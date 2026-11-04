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
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/labels"
	"github.com/DawnMagnet/wslc-compose-go/internal/paths"
	"github.com/DawnMagnet/wslc-compose-go/internal/plan"
	"github.com/DawnMagnet/wslc-compose-go/internal/project"
	"github.com/DawnMagnet/wslc-compose-go/internal/state"
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
	StateDir    string        // root for generated secrets/configs; empty uses the user config dir
}

// App executes compose use cases against one wslc client.
type App struct {
	o      Options
	c      *wslc.Client
	paths  paths.Mapper
	io     wslc.IO
	eng    project.Engine
	engSet bool
	noted  bool
	caps   wslc.Capabilities
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
	eng := a.engine(ctx)
	return p, project.Enforce(project.CheckEngine(p, eng), a.o.Strict, w)
}

// engine probes `wslc --version` once. A dry-run with no binary plans the
// current feature set so the printed commands stay complete; a real run with
// an unreadable version disables gated features instead of sending flags the
// binary will reject.
func (a *App) engine(ctx context.Context) project.Engine {
	if a.engSet {
		return a.eng
	}
	a.caps = wslc.ParseCapabilities(a.c.Version(ctx))
	if !a.caps.Known && a.o.DryRun {
		a.caps = wslc.AssumeCurrent()
	}
	a.eng = project.Engine{
		MultiNetwork:   a.caps.AtLeast(wslc.VerEndpoints),
		StaticIPv4:     a.caps.AtLeast(wslc.VerEndpoints),
		IPRange:        a.caps.AtLeast(wslc.VerEndpoints),
		EndpointExtras: a.caps.AtLeast(wslc.VerEndpoints),
		BuildSecret:    a.caps.AtLeast(wslc.VerBuildSecret),
		HostGateway:    a.caps.AtLeast(wslc.VerHostGateway),
	}
	a.engSet = true
	return a.eng
}

// noteEngine warns once when a real run could not read wslc's version and
// therefore left gated features off. `config` stays quiet: it never calls this.
func (a *App) noteEngine() {
	if a.noted || a.caps.Known || a.caps.Assumed {
		return
	}
	a.noted = true
	a.infof("WARN wslc version unknown; multi-network attach, static IPv4, IP ranges, build --secret and host-gateway port loopback are disabled")
}

// projectState is the directory that holds generated secret and config files
// for one project. It is removed by down.
func (a *App) projectState(project string) (string, error) {
	root := a.o.StateDir
	if root == "" {
		var err error
		root, err = defaultStateRoot()
		if err != nil {
			root = os.TempDir()
		}
		root = filepath.Join(root, "wslc-compose")
	}
	project = strings.Map(func(r rune) rune {
		if r == os.PathSeparator || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, project)
	if project == "" || project == "." || project == ".." {
		return "", fmt.Errorf("invalid project name %q", project)
	}
	return filepath.Join(root, project), nil
}

// defaultStateRoot is %LOCALAPPDATA% on Windows (not Roaming AppData) and
// the user config directory everywhere else.
func defaultStateRoot() (string, error) {
	if runtime.GOOS == "windows" {
		return os.UserCacheDir()
	}
	return os.UserConfigDir()
}

// prepareTranslator resolves secret and config files, then returns the
// translator that mounts them. Dry-run computes the mount paths and does
// not write secret bytes. rb, when non-nil, records a new state directory
// so a failed up deletes it.
func (a *App) prepareTranslator(p *types.Project, rb *rollback) (*translate.Translator, error) {
	t := a.translator(p)
	dir, err := a.projectState(p.Name)
	if err != nil {
		return nil, err
	}
	write := !a.c.DryRun()
	prep, created, err := state.Prepare(dir, p, write)
	if created && rb != nil {
		rb.add("State", dir, func(context.Context, string) error { return state.Remove(dir) })
	}
	if err != nil {
		return nil, err
	}
	t.SecretFiles = prep.SecretFiles
	if len(prep.Mounts) > 0 {
		t.Mounts = map[string][]translate.FileMount{}
		for svc, ms := range prep.Mounts {
			for _, m := range ms {
				t.Mounts[svc] = append(t.Mounts[svc], translate.FileMount{Source: m.Source, Target: m.Target})
			}
		}
	}
	return t, nil
}

func (a *App) translator(p *types.Project) *translate.Translator {
	files := make([]string, len(p.ComposeFiles))
	for i, f := range p.ComposeFiles {
		files[i] = a.paths(f)
	}
	return &translate.Translator{
		Project:           p,
		Paths:             a.paths,
		DefaultDNS:        a.o.DefaultDNS,
		OmitStaticIP:      a.engSet && !a.eng.StaticIPv4,
		AllowBuildSecrets: !a.engSet || a.eng.BuildSecret,
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
