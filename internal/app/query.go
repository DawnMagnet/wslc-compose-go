package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/compose-spec/compose-go/v2/types"
	"golang.org/x/sync/errgroup"

	"github.com/dawnmagnet/wslc-compose-go/internal/logs"
	"github.com/dawnmagnet/wslc-compose-go/internal/project"
	"github.com/dawnmagnet/wslc-compose-go/internal/wslc"
)

// PsOptions are the flags of `ps`.
type PsOptions struct {
	Services     []string
	All          bool
	Quiet        bool
	ListServices bool     // --services: print service names only
	Status       []string // --status: keep containers in these states (implies All)
	Format       string   // table|json
}

// Ps lists the project's containers.
func (a *App) Ps(ctx context.Context, o PsOptions) error {
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	cs, err := a.c.Containers(ctx, p.Name, o.All || len(o.Status) > 0)
	if err != nil {
		return err
	}
	cs = filter(cs, o.Services)
	if len(o.Status) > 0 {
		cs = slices.DeleteFunc(cs, func(c wslc.Container) bool { return !slices.Contains(o.Status, c.State) })
	}
	svcOrder, _ := allOrder(p, false)
	sortContainers(cs, svcOrder)
	a.fillHealth(ctx, p, cs)
	out := a.io.Stdout
	switch {
	case o.ListServices:
		var seen []string
		for _, c := range cs {
			if !slices.Contains(seen, c.Service()) {
				seen = append(seen, c.Service())
				fmt.Fprintln(out, c.Service())
			}
		}
	case o.Quiet:
		for _, c := range cs {
			fmt.Fprintln(out, firstNonEmpty(c.ID, c.Name))
		}
	case o.Format == "json":
		type row struct {
			ID, Name, Service, Image, State, Health, Ports string
			Number                                         int
		}
		rows := make([]row, 0, len(cs))
		for _, c := range cs {
			rows = append(rows, row{c.ID, c.Name, c.Service(), c.Image, c.State, c.Health, c.Ports, c.Number()})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false) // keep "0.0.0.0:80->80/tcp" readable
		return enc.Encode(rows)
	default:
		health := slices.ContainsFunc(cs, func(c wslc.Container) bool { return c.Health != "" })
		tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		row := func(cols ...string) {
			if !health {
				cols = slices.Delete(cols, 4, 5)
			}
			fmt.Fprintln(tw, strings.Join(cols, "\t"))
		}
		row("NAME", "IMAGE", "SERVICE", "STATUS", "HEALTH", "PORTS")
		for _, c := range cs {
			row(c.Name, c.Image, c.Service(), c.State, c.Health, c.Ports)
		}
		return tw.Flush()
	}
	return nil
}

// fillHealth inspects running containers of services that declare a
// healthcheck when `wslc list` did not report their health.
func (a *App) fillHealth(ctx context.Context, p *types.Project, cs []wslc.Container) {
	g, gctx := errgroup.WithContext(ctx)
	for i, c := range cs {
		hc := p.Services[c.Service()].HealthCheck
		if c.Health != "" || !c.Running() || hc == nil || hc.Disable {
			continue
		}
		g.Go(func() error {
			if full, err := a.c.Inspect(gctx, firstNonEmpty(c.ID, c.Name)); err == nil {
				cs[i].Health = full.Health
			}
			return nil
		})
	}
	_ = g.Wait()
}

// LogsOptions are the flags of `logs`.
type LogsOptions struct {
	Services []string
	wslc.LogOptions
	NoPrefix bool
}

// Logs prints (or follows) the multiplexed logs of the project's containers.
func (a *App) Logs(ctx context.Context, o LogsOptions) error {
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	cs, err := a.containers(ctx, p, true)
	if err != nil {
		return err
	}
	cs = filter(cs, o.Services)
	svcOrder, _ := allOrder(p, false)
	sortContainers(cs, svcOrder)
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return a.follow(ctx, names, o.LogOptions, !o.NoPrefix)
}

// follow streams the logs of the named containers concurrently.
func (a *App) follow(ctx context.Context, names []string, o wslc.LogOptions, prefix bool) error {
	if len(names) == 0 {
		a.infof("No containers found")
		return nil
	}
	mux := logs.New(a.io.Stdout, names, a.o.Color, prefix)
	g, ctx := errgroup.WithContext(ctx)
	for _, n := range names {
		w := mux.Writer(n)
		g.Go(func() error {
			defer w.Close()
			err := a.c.Logs(ctx, n, o, wslc.IO{Stdout: w, Stderr: w})
			if ctx.Err() != nil {
				return nil // interrupted: not an error
			}
			return err
		})
	}
	return g.Wait()
}

// attach follows logs after a foreground `up`; on interrupt it stops the
// containers like `docker compose up` does.
func (a *App) attach(ctx context.Context, names []string, timeout int) error {
	a.infof("Attaching to %s (Ctrl+C to stop)", strings.Join(names, ", "))
	err := a.follow(ctx, names, wslc.LogOptions{Follow: true}, true)
	if ctx.Err() == nil {
		return err
	}
	a.infof("Gracefully stopping... (press Ctrl+C again to force)")
	stopCtx := context.WithoutCancel(ctx)
	for i := len(names) - 1; i >= 0; i-- {
		if err := a.c.Stop(stopCtx, names[i], timeout); err != nil {
			a.infof("WARN stop %s: %v", names[i], err)
		}
	}
	return nil
}

// ConfigOptions are the flags of `config`.
type ConfigOptions struct {
	Services bool   // list service names
	Volumes  bool   // list volume names
	Networks bool   // list network names
	Images   bool   // list image names
	Hash     string // "*" or comma separated services: print config hashes
	Format   string // yaml|json
}

// Config prints the normalized model or one of its projections.
func (a *App) Config(ctx context.Context, o ConfigOptions) error {
	listing := o.Services || o.Volumes || o.Networks || o.Images || o.Hash != ""
	p, err := a.loadRaw(ctx, nil, false, listing) // listings stay machine-readable
	if err != nil {
		return err
	}
	out := a.io.Stdout
	lines := func(ss []string) error {
		for _, s := range ss {
			fmt.Fprintln(out, s)
		}
		return nil
	}
	switch {
	case o.Services:
		return lines(p.ServiceNames())
	case o.Volumes:
		return lines(p.VolumeNames())
	case o.Networks:
		return lines(p.NetworkNames())
	case o.Images:
		var imgs []string
		for _, n := range p.ServiceNames() {
			imgs = append(imgs, imageOf(p, n))
		}
		return lines(imgs)
	case o.Hash != "":
		project.NameAnonymousVolumes(p) // hash exactly what `up` deploys
		names := p.ServiceNames()
		if o.Hash != "*" {
			names = strings.Split(o.Hash, ",")
		}
		for _, n := range names {
			s, err := p.GetService(strings.TrimSpace(n))
			if err != nil {
				return err
			}
			h, err := project.ServiceHash(s)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", s.Name, h)
		}
		return nil
	}
	var b []byte
	if o.Format == "json" {
		b, err = p.MarshalJSON()
	} else {
		b, err = p.MarshalYAML()
	}
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}
