package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/state"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

// DownOptions are the flags of `down`.
type DownOptions struct {
	Volumes       bool // also remove named volumes declared by the project
	RemoveOrphans bool
	Timeout       int
}

// Down stops and removes the project's containers in reverse dependency
// order, then its networks and (optionally) volumes. External resources are
// never touched.
func (a *App) Down(ctx context.Context, o DownOptions) error {
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	svcOrder, err := allOrder(p, true)
	if err != nil {
		return err
	}
	all, err := a.c.Containers(ctx, p.Name, true)
	if err != nil {
		return err
	}
	var targets []wslc.Container
	var orphans []string
	for _, c := range all {
		if c.OneOff() || slices.Contains(svcOrder, c.Service()) || o.RemoveOrphans {
			targets = append(targets, c)
		} else {
			orphans = append(orphans, c.Name)
		}
	}
	if len(orphans) > 0 {
		a.infof("WARN Found orphan containers %v; use --remove-orphans to remove them", orphans)
	}
	sortContainers(targets, svcOrder)
	programmed, err := a.gatewayProgrammed(p.Name)
	if err != nil {
		return err
	}
	// On 3.0.3 the cleanup script is idempotent, so run it whenever the
	// compose file uses host-gateway, a container is labeled, or the marker
	// says rules were programmed. Skip only when none of those are true: an
	// empty down of a project that does not use host-gateway makes no
	// session call. An older or unknown wslc cannot run the script; keep
	// the marker instead of deleting the only record that rules may exist.
	keepMarker := false
	needCleanup := false
	if programmed && !a.eng.HostGateway {
		a.infof("WARN host-gateway: rules may still be installed, but this wslc cannot run session cleanup (needs %s); keeping the marker", wslc.VerHostGateway)
		keepMarker = true
	} else if a.eng.HostGateway && (projectUsesGateway(p) || programmed || sawHostGateway(all)) {
		needCleanup = true
	}
	for _, c := range targets {
		a.infof("Container %s  Removing", c.Name)
		if c.Running() {
			if err := a.c.Stop(ctx, c.Name, o.Timeout); err != nil {
				return err
			}
		}
		if err := a.c.Remove(ctx, c.Name); err != nil {
			return err
		}
	}
	var errs []error
	var cleanupErr error
	if needCleanup {
		if err := a.reconcileGateway(ctx, p, true); err != nil {
			a.infof("WARN host-gateway cleanup: %v", err)
			cleanupErr = err
			errs = append(errs, err)
		}
	}
	if err := a.removeResources(ctx, p, o.Volumes); err != nil {
		errs = append(errs, err)
	}
	if err := a.removeState(p.Name); err != nil {
		errs = append(errs, err)
	}
	// Containers and the state directory are already gone. Put the marker
	// back when cleanup failed, or when this wslc could not attempt it.
	if cleanupErr != nil || keepMarker {
		if err := a.noteGatewayProgrammed(p.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *App) removeState(project string) error {
	dir, err := a.projectState(project)
	if err != nil {
		return err
	}
	if err := state.Remove(dir); err != nil {
		a.infof("WARN could not remove generated secrets/configs in %s: %v", dir, err)
		return err
	}
	return nil
}

func (a *App) removeResources(ctx context.Context, p *types.Project, volumes bool) error {
	nets, err := a.c.Networks(ctx)
	if err != nil {
		return err
	}
	for _, key := range p.NetworkNames() {
		n := p.Networks[key]
		name := firstNonEmpty(n.Name, key)
		if bool(n.External) || !(nets[name] || a.c.DryRun()) {
			continue
		}
		a.infof("Network %s  Removing", name)
		if err := a.c.RemoveNetwork(ctx, name); err != nil {
			a.infof("WARN could not remove network %s: %v", name, err)
		}
	}
	if !volumes {
		return nil
	}
	vols, err := a.c.Volumes(ctx)
	if err != nil {
		return err
	}
	for _, key := range p.VolumeNames() {
		v := p.Volumes[key]
		name := firstNonEmpty(v.Name, key)
		if bool(v.External) || !(vols[name] || a.c.DryRun()) {
			continue
		}
		a.infof("Volume %s  Removing", name)
		if err := a.c.RemoveVolume(ctx, name); err != nil {
			a.infof("WARN could not remove volume %s: %v", name, err)
		}
	}
	return nil
}

// Start starts existing stopped containers of the selected services.
func (a *App) Start(ctx context.Context, services []string) error {
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	if err := a.each(ctx, services, false, func(c wslc.Container) error {
		if c.Running() {
			return nil
		}
		a.infof("Container %s  Starting", c.Name)
		if err := a.c.Start(ctx, c.Name); err != nil {
			return err
		}
		if s, ok := p.Services[c.Service()]; ok {
			return a.applyExtraHosts(ctx, s, c.Name)
		}
		return nil
	}); err != nil {
		return err
	}
	return a.reconcileGateway(ctx, p, false)
}

// Stop stops running containers of the selected services (dependents first).
func (a *App) Stop(ctx context.Context, services []string, timeout int) error {
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	if err := a.each(ctx, services, true, func(c wslc.Container) error {
		if !c.Running() {
			return nil
		}
		a.infof("Container %s  Stopping", c.Name)
		return a.c.Stop(ctx, c.Name, timeout)
	}); err != nil {
		return err
	}
	return a.reconcileGateway(ctx, p, false)
}

// Restart stops then starts containers; wslc has no native restart.
func (a *App) Restart(ctx context.Context, services []string, timeout int) error {
	if err := a.Stop(ctx, services, timeout); err != nil {
		return err
	}
	return a.each(ctx, services, false, func(c wslc.Container) error {
		a.infof("Container %s  Starting", c.Name)
		return a.c.Start(ctx, c.Name)
	})
}

// each applies fn to the selected services' containers in dependency order.
func (a *App) each(ctx context.Context, services []string, reverse bool, fn func(wslc.Container) error) error {
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	svcOrder, err := allOrder(p, reverse)
	if err != nil {
		return err
	}
	for _, s := range services {
		if !slices.Contains(svcOrder, s) {
			return fmt.Errorf("no such service: %s", s)
		}
	}
	cs, err := a.containers(ctx, p, true)
	if err != nil {
		return err
	}
	cs = filter(cs, services)
	if len(cs) == 0 {
		a.infof("No containers found")
		return nil
	}
	sortContainers(cs, svcOrder)
	for _, c := range cs {
		if err := fn(c); err != nil {
			return err
		}
	}
	return nil
}
