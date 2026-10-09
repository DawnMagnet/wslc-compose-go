package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"golang.org/x/sync/errgroup"

	"github.com/dawnmagnet/wslc-compose-go/internal/logs"
	"github.com/dawnmagnet/wslc-compose-go/internal/project"
	"github.com/dawnmagnet/wslc-compose-go/internal/wslc"
)

// PsOptions are the flags of `ps`.
type PsOptions struct {
	Services []string
	All      bool
	Quiet    bool
	Format   string // table|json
}

// Ps lists the project's containers.
func (a *App) Ps(ctx context.Context, o PsOptions) error {
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	cs, err := a.c.Containers(ctx, p.Name, o.All)
	if err != nil {
		return err
	}
	cs = filter(cs, o.Services)
	svcOrder, _ := allOrder(p, false)
	sortContainers(cs, svcOrder)
	out := a.io.Stdout
	switch {
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
		return enc.Encode(rows)
	default:
		tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		fmt.Fprintln(tw, "NAME\tIMAGE\tSERVICE\tSTATUS\tPORTS")
		for _, c := range cs {
			status := c.State
			if c.Health != "" {
				status += " (" + c.Health + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.Image, c.Service(), status, c.Ports)
		}
		return tw.Flush()
	}
	return nil
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
	p, err := a.load(ctx, nil, false, false)
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
