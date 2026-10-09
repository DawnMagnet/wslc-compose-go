// Package app wires the loader, translator, planner and wslc client into
// the use cases behind each CLI command. Commands live in one file per
// concern (up, down, lifecycle, query, exec, images).
package app

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/labels"
	"github.com/DawnMagnet/wslc-compose-go/internal/paths"
	"github.com/DawnMagnet/wslc-compose-go/internal/plan"
	"github.com/DawnMagnet/wslc-compose-go/internal/project"
	"github.com/DawnMagnet/wslc-compose-go/internal/translate"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

// Options are the global flags shared by every command.
type Options struct {
	Files       []string
	ProjectName string
	ProjectDir  string
	Profiles    []string
	EnvFiles    []string
	DryRun      bool
	Strict      bool
	Bin         string        // wslc binary; empty means auto-detect
	DefaultDNS  []string      // DNS used when a service sets none; empty disables
	Parallel    int           // concurrent short wslc calls
	Color       bool          // colored log prefixes
	PollEvery   time.Duration // dependency wait poll interval (default 1s)
	Env         []string      // extra environment for interpolation (tests)
	IgnoreOSEnv bool          // hermetic loading (tests)
}

// App executes compose use cases against one wslc client.
type App struct {
	o     Options
	c     *wslc.Client
	paths paths.Mapper
	io    wslc.IO
}

// New builds an App on top of an explicit Runner and path Mapper; tests use
// it with a fake runner. Dry-run commands go to stdout, progress to stderr.
func New(o Options, r wslc.Runner, m paths.Mapper, stdio wslc.IO) *App {
	if o.PollEvery == 0 {
		o.PollEvery = time.Second
	}
	if stdio.Stdout == nil {
		stdio.Stdout = io.Discard
	}
	if stdio.Stderr == nil {
		stdio.Stderr = io.Discard
	}
	log := stdio.Stderr
	if o.DryRun {
		log = stdio.Stdout
	}
	bin := o.Bin
	if bin == "" {
		bin = "wslc"
	}
	c := wslc.New(r, wslc.Options{Bin: bin, DryRun: o.DryRun, Log: log, Parallel: o.Parallel})
	return &App{o: o, c: c, paths: m, io: stdio}
}

// Default builds an App that drives the real wslc binary with OS stdio.
func Default(o Options) *App {
	o.Bin = wslc.Find(o.Bin)
	return New(o, wslc.Exec{Bin: o.Bin}, paths.For(o.Bin),
		wslc.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
}

// infof prints a progress line to stderr.
func (a *App) infof(format string, args ...any) {
	fmt.Fprintf(a.io.Stderr, format+"\n", args...)
}

// load reads the project, applies the unsupported-field policy (issues go
// to stderr unless quiet) and names anonymous volumes for wslc.
func (a *App) load(ctx context.Context, services []string, noDeps, quiet bool) (*types.Project, error) {
	p, err := a.loadRaw(ctx, services, noDeps, quiet)
	if err != nil {
		return nil, err
	}
	project.NameAnonymousVolumes(p)
	return p, nil
}

// loadRaw is load without wslc-specific rewrites (used by `config`).
func (a *App) loadRaw(ctx context.Context, services []string, noDeps, quiet bool) (*types.Project, error) {
	p, err := project.Load(ctx, project.Options{
		Files: a.o.Files, Name: a.o.ProjectName, WorkDir: a.o.ProjectDir,
		EnvFiles: a.o.EnvFiles, Profiles: a.o.Profiles, Services: services, NoDeps: noDeps,
		Env: a.o.Env, IgnoreOSEnv: a.o.IgnoreOSEnv,
	})
	if err != nil {
		return nil, err
	}
	w := a.io.Stderr
	if quiet {
		w = io.Discard
	}
	return p, project.Enforce(project.Check(p), a.o.Strict, w)
}

func (a *App) translator(p *types.Project) *translate.Translator {
	files := make([]string, len(p.ComposeFiles))
	for i, f := range p.ComposeFiles {
		files[i] = a.paths(f)
	}
	return &translate.Translator{
		Project:    p,
		Paths:      a.paths,
		DefaultDNS: a.o.DefaultDNS,
		Meta: labels.Meta{
			Project: p.Name, WorkingDir: a.paths(p.WorkingDir),
			ConfigFiles: files, Version: Version,
		},
	}
}

// containers lists the project's non one-off containers.
func (a *App) containers(ctx context.Context, p *types.Project, all bool) ([]wslc.Container, error) {
	cs, err := a.c.Containers(ctx, p.Name, all)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(cs, wslc.Container.OneOff), nil
}

func actuals(cs []wslc.Container) []plan.Actual {
	out := make([]plan.Actual, len(cs))
	for i, c := range cs {
		out[i] = plan.Actual{Name: c.Name, Service: c.Service(), Number: c.Number(), Hash: c.Hash(), Running: c.Running()}
	}
	return out
}

// order returns enabled service names in dependency order.
func order(p *types.Project, reverse bool) ([]string, error) {
	return plan.Order(depMap(p.Services), reverse)
}

// allOrder includes profile-disabled services (used by down/stop).
func allOrder(p *types.Project, reverse bool) ([]string, error) {
	all := maps.Clone(p.Services)
	maps.Copy(all, p.DisabledServices)
	return plan.Order(depMap(all), reverse)
}

func depMap(s types.Services) map[string][]string {
	m := make(map[string][]string, len(s))
	for name, svc := range s {
		m[name] = slices.Sorted(maps.Keys(svc.DependsOn))
	}
	return m
}

// replicaNames returns the container names of all replicas of a service.
func replicaNames(p *types.Project, s types.ServiceConfig) ([]string, error) {
	n := s.GetScale()
	if s.ContainerName != "" && n > 1 {
		return nil, fmt.Errorf("service %q: container_name cannot be combined with %d replicas", s.Name, n)
	}
	names := make([]string, n)
	for i := range names {
		names[i] = translate.ContainerName(p, s, i+1)
	}
	return names, nil
}

// filter keeps containers that belong to one of services (all when empty).
func filter(cs []wslc.Container, services []string) []wslc.Container {
	if len(services) == 0 {
		return cs
	}
	return slices.DeleteFunc(slices.Clone(cs), func(c wslc.Container) bool {
		return !slices.Contains(services, c.Service())
	})
}

// sortContainers orders containers by the given service order then number.
func sortContainers(cs []wslc.Container, svcOrder []string) {
	idx := func(s string) int {
		if i := slices.Index(svcOrder, s); i >= 0 {
			return i
		}
		return len(svcOrder)
	}
	slices.SortStableFunc(cs, func(x, y wslc.Container) int {
		if d := idx(x.Service()) - idx(y.Service()); d != 0 {
			return d
		}
		if x.Service() != y.Service() {
			if x.Service() < y.Service() {
				return -1
			}
			return 1
		}
		return x.Number() - y.Number()
	})
}
