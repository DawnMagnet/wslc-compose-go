package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/dawnmagnet/wslc-compose-go/internal/labels"
	"github.com/dawnmagnet/wslc-compose-go/internal/plan"
	"github.com/dawnmagnet/wslc-compose-go/internal/project"
	"github.com/dawnmagnet/wslc-compose-go/internal/translate"
	"github.com/dawnmagnet/wslc-compose-go/internal/wslc"
)

// UpOptions are the flags of `up`.
type UpOptions struct {
	Services      []string
	Detach        bool
	Build         bool   // always build images with a build section
	NoBuild       bool   // never build
	Pull          string // override pull_policy: always|missing|never
	ForceRecreate bool
	NoRecreate    bool
	NoDeps        bool
	RemoveOrphans bool
	Wait          bool           // after -d, wait until services are running/healthy
	WaitTimeout   time.Duration  // bound for dependency conditions and --wait
	Scale         map[string]int // --scale SERVICE=N overrides scale/deploy.replicas
	Timeout       int            // stop timeout in seconds (-1 = wslc default)
}

// Up creates networks/volumes, prepares images and converges containers in
// dependency order. Without -d it then follows logs and stops on Ctrl+C.
func (a *App) Up(ctx context.Context, o UpOptions) error {
	started, err := a.converge(ctx, o)
	if err != nil {
		return err
	}
	if o.Wait {
		for _, name := range started {
			if err := a.waitFor(ctx, name, waitReady, o.WaitTimeout); err != nil {
				return err
			}
		}
	}
	if o.Detach || a.c.DryRun() || len(started) == 0 {
		return nil
	}
	return a.attach(ctx, started, o.Timeout)
}

// converge brings the project to its desired state and returns the
// containers that should now be running. On failure everything it created
// (containers, networks, volumes) is rolled back, best effort.
func (a *App) converge(ctx context.Context, o UpOptions) (started []string, err error) {
	p, err := a.load(ctx, o.Services, o.NoDeps, false)
	if err != nil {
		return nil, err
	}
	if err := applyScale(p, o.Scale); err != nil {
		return nil, err
	}
	rb := &rollback{}
	defer func() {
		if err != nil && !a.c.DryRun() {
			rb.run(context.WithoutCancel(ctx), a)
		}
	}()
	if err := a.ensureResources(ctx, p, rb); err != nil {
		return nil, err
	}
	rebuilt, err := a.prepareImages(ctx, p, imageOptions{build: o.Build, noBuild: o.NoBuild, pull: o.Pull})
	if err != nil {
		return nil, err
	}
	cs, err := a.containers(ctx, p, true)
	if err != nil {
		return nil, err
	}
	actual := actuals(cs)
	names, err := order(p, false)
	if err != nil {
		return nil, err
	}
	t := a.translator(p)
	for _, name := range names {
		s := p.Services[name]
		if err := a.waitDeps(ctx, p, s, o.WaitTimeout); err != nil {
			return nil, err
		}
		hash, err := project.ServiceHash(s)
		if err != nil {
			return nil, err
		}
		replicas, err := replicaNames(p, s)
		if err != nil {
			return nil, err
		}
		ops := plan.Service(plan.Desired{Service: name, Hash: hash, Names: replicas}, actual,
			plan.Options{ForceRecreate: o.ForceRecreate || rebuilt[name], NoRecreate: o.NoRecreate})
		for _, op := range ops {
			if op.Action == plan.Create || op.Action == plan.Recreate {
				// Registered first: a failed `run` may still leave a container behind.
				rb.add("Container", op.Name, a.c.Remove)
			}
			if err := a.apply(ctx, t, s, hash, op, o.Timeout); err != nil {
				return nil, err
			}
			if op.Action != plan.Remove {
				started = append(started, op.Name)
			}
		}
	}
	return started, a.handleOrphans(ctx, p, actual, o.RemoveOrphans, o.Timeout)
}

// applyScale overrides replica counts from `up --scale SERVICE=N`.
func applyScale(p *types.Project, scale map[string]int) error {
	for name, n := range scale {
		s, ok := p.Services[name]
		if !ok {
			return fmt.Errorf("--scale: no such service: %s", name)
		}
		if n < 0 {
			return fmt.Errorf("--scale %s: replicas must be >= 0", name)
		}
		s.SetScale(n)
		p.Services[name] = s
	}
	return nil
}

// rollback records undo steps for resources created by a failing `up`.
// A nil *rollback ignores registrations (callers that never roll back).
type rollback struct{ steps []undo }

type undo struct {
	kind, name string
	remove     func(context.Context, string) error
}

func (r *rollback) add(kind, name string, remove func(context.Context, string) error) {
	if r != nil {
		r.steps = append(r.steps, undo{kind, name, remove})
	}
}

// run undoes the recorded steps in reverse order, reporting but not failing
// on errors (a container whose `run` failed may simply not exist).
func (r *rollback) run(ctx context.Context, a *App) {
	if len(r.steps) == 0 {
		return
	}
	a.infof("Rolling back resources created by this run")
	for _, u := range slices.Backward(r.steps) {
		a.infof("%s %s  Removing", u.kind, u.name)
		if err := u.remove(ctx, u.name); err != nil && u.kind != "Container" {
			a.infof("WARN rollback %s %s: %v", strings.ToLower(u.kind), u.name, err)
		}
	}
}

// apply executes one planned operation.
func (a *App) apply(ctx context.Context, t *translate.Translator, s types.ServiceConfig, hash string, op plan.Op, timeout int) error {
	run := func() error {
		return a.c.Run(ctx, t.Run(s, translate.RunOptions{Number: op.Number, Hash: hash, Detach: true, Publish: true}))
	}
	remove := func() error {
		if op.Running {
			if err := a.c.Stop(ctx, op.Name, timeout); err != nil {
				return err
			}
		}
		return a.c.Remove(ctx, op.Name)
	}
	switch op.Action {
	case plan.Keep:
		a.infof("Container %s  Running", op.Name)
		return nil
	case plan.Start:
		a.infof("Container %s  Starting", op.Name)
		return a.c.Start(ctx, op.Name)
	case plan.Create:
		a.infof("Container %s  Creating", op.Name)
		return run()
	case plan.Recreate:
		a.infof("Container %s  Recreating", op.Name)
		if err := remove(); err != nil {
			return err
		}
		return run()
	case plan.Remove:
		a.infof("Container %s  Removing", op.Name)
		return remove()
	}
	return fmt.Errorf("unknown action %v", op.Action)
}

func (a *App) handleOrphans(ctx context.Context, p *types.Project, actual []plan.Actual, remove bool, timeout int) error {
	known := append(p.ServiceNames(), slices.Collect(maps.Keys(p.DisabledServices))...)
	orphans := plan.Orphans(known, actual)
	if len(orphans) == 0 {
		return nil
	}
	if !remove {
		a.infof("WARN Found orphan containers %v for this project; use --remove-orphans to clean them up", orphanNames(orphans))
		return nil
	}
	for _, o := range orphans {
		if err := a.apply(ctx, nil, types.ServiceConfig{}, "", plan.Op{Action: plan.Remove, Name: o.Name, Running: o.Running}, timeout); err != nil {
			return err
		}
	}
	return nil
}

func orphanNames(as []plan.Actual) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}

// ensureResources creates the networks and named volumes used by the
// selected services; external ones must already exist.
// Created resources are registered with rb (may be nil) for rollback.
func (a *App) ensureResources(ctx context.Context, p *types.Project, rb *rollback) error {
	nets, vols := map[string]bool{}, map[string]bool{}
	for _, s := range p.Services {
		if key := translate.Network(s); key != "" {
			nets[key] = true
		}
		for _, v := range s.Volumes {
			if v.Type == types.VolumeTypeVolume && v.Source != "" {
				vols[v.Source] = true
			}
		}
	}
	if len(nets) > 0 {
		existing, err := a.c.Networks(ctx)
		if err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(nets)) {
			n := p.Networks[key]
			name := firstNonEmpty(n.Name, key)
			switch {
			case existing[name]:
			case bool(n.External):
				if !a.c.DryRun() {
					return fmt.Errorf("external network %q not found", name)
				}
			default:
				a.infof("Network %s  Creating", name)
				spec := wslc.NetworkSpec{
					Driver: n.Driver, Internal: n.Internal, Options: n.DriverOpts,
					Labels: labels.Merge(n.Labels, map[string]string{labels.Project: p.Name, labels.Network: key}),
				}
				if len(n.Ipam.Config) > 0 && n.Ipam.Config[0] != nil {
					spec.Subnet, spec.Gateway = n.Ipam.Config[0].Subnet, n.Ipam.Config[0].Gateway
				}
				if err := a.c.CreateNetwork(ctx, name, spec); err != nil {
					return err
				}
				rb.add("Network", name, a.c.RemoveNetwork)
			}
		}
	}
	if len(vols) > 0 {
		existing, err := a.c.Volumes(ctx)
		if err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(vols)) {
			v := p.Volumes[key]
			name := firstNonEmpty(v.Name, key)
			switch {
			case existing[name]:
			case bool(v.External):
				if !a.c.DryRun() {
					return fmt.Errorf("external volume %q not found", name)
				}
			default:
				a.infof("Volume %s  Creating", name)
				l := labels.Merge(v.Labels, map[string]string{labels.Project: p.Name, labels.Volume: key})
				if err := a.c.CreateVolume(ctx, name, l); err != nil {
					return err
				}
				rb.add("Volume", name, a.c.RemoveVolume)
			}
		}
	}
	return nil
}

// waitDeps blocks until every dependency of s satisfies its condition.
func (a *App) waitDeps(ctx context.Context, p *types.Project, s types.ServiceConfig, timeout time.Duration) error {
	for _, dep := range slices.Sorted(maps.Keys(s.DependsOn)) {
		ds, ok := p.Services[dep]
		if !ok {
			continue
		}
		cond := waitStarted
		switch s.DependsOn[dep].Condition {
		case types.ServiceConditionHealthy:
			cond = waitHealthy
		case types.ServiceConditionCompletedSuccessfully:
			cond = waitCompleted
		default:
			continue // service_started: the dependency was started just before
		}
		names, err := replicaNames(p, ds)
		if err != nil {
			return err
		}
		for _, n := range names {
			if err := a.waitFor(ctx, n, cond, timeout); err != nil {
				if !s.DependsOn[dep].Required {
					a.infof("WARN optional dependency %s: %v", dep, err)
					continue
				}
				return err
			}
		}
	}
	return nil
}

type waitCond string

const (
	waitStarted   waitCond = "started"
	waitHealthy   waitCond = "healthy"
	waitCompleted waitCond = "completed successfully"
	waitReady     waitCond = "ready" // healthy when a health state exists, else running
)

// errNoHealth signals that wslc reports no health state for a container.
var errNoHealth = errors.New("no health state reported")

// waitFor polls `wslc inspect` until name satisfies cond or timeout expires.
func (a *App) waitFor(ctx context.Context, name string, cond waitCond, timeout time.Duration) error {
	if a.c.DryRun() {
		fmt.Fprintf(a.io.Stdout, "# wait for %s to be %s\n", name, cond)
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	a.infof("Container %s  Waiting (%s)", name, cond)
	noHealth := 0
	for {
		c, err := a.c.Inspect(ctx, name)
		if err == nil {
			done, err := satisfied(c, cond)
			switch {
			case errors.Is(err, errNoHealth):
				// Health may simply not be populated yet; after a few polls
				// degrade to "running", because some wslc builds omit it.
				if noHealth++; noHealth >= 3 {
					a.infof("WARN %s: wslc reports no health status; treating running as healthy", name)
					return nil
				}
			case err != nil:
				return fmt.Errorf("container %s: %w", name, err)
			case done:
				a.infof("Container %s  %s", name, cond)
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("container %s: timed out waiting to be %s", name, cond)
		case <-time.After(a.o.PollEvery):
		}
	}
}

func satisfied(c wslc.Container, cond waitCond) (bool, error) {
	switch cond {
	case waitHealthy, waitReady:
		switch c.Health {
		case "healthy":
			return true, nil
		case "unhealthy":
			return false, errors.New("is unhealthy")
		case "", "none":
			if cond == waitReady {
				return c.Running(), nil
			}
			if c.Running() {
				return false, errNoHealth
			}
			return false, nil
		}
		return false, nil
	case waitCompleted:
		if c.Running() || c.State == "" || c.State == "created" {
			return false, nil
		}
		if c.ExitCode != 0 {
			return false, fmt.Errorf("exited with code %d", c.ExitCode)
		}
		return true, nil
	default:
		return c.Running(), nil
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
