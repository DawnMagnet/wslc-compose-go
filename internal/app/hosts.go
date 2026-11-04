package app

import (
	"context"
	"fmt"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/labels"
	"github.com/DawnMagnet/wslc-compose-go/internal/state"
	"github.com/DawnMagnet/wslc-compose-go/internal/translate"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

// applyExtraHosts appends the service's extra_hosts after the container is
// running. wslc has no --add-host. Failures are warnings unless --strict.
func (a *App) applyExtraHosts(ctx context.Context, s types.ServiceConfig, name string) error {
	if len(s.ExtraHosts) == 0 {
		return nil
	}
	script, err := translate.ExtraHostsScript(s.ExtraHosts)
	if err != nil {
		return err
	}
	if script == "" {
		return nil
	}
	err = a.c.Exec(ctx, []string{"exec", "-u", "0", name, "sh", "-c", script}, wslc.IO{})
	if err != nil {
		a.infof("WARN %s: extra_hosts: %v", name, err)
		if a.o.Strict {
			return err
		}
	}
	return nil
}

// reconcileGateway installs or removes the project's host-gateway DNAT rules.
// cleanup drops the chains. A build older than 3.0.3.0 keeps the /etc/hosts
// update and skips the session script, with a warning.
func (a *App) reconcileGateway(ctx context.Context, p *types.Project, cleanup bool) error {
	wants := projectUsesGateway(p)
	if !wants && !cleanup {
		return nil
	}
	if !a.eng.HostGateway {
		if wants && !cleanup {
			a.infof("WARN host-gateway published-port loopback needs wslc %s or newer; /etc/hosts is still updated", wslc.VerHostGateway)
		}
		return nil
	}
	var routes []translate.Route
	if !cleanup {
		live, err := a.liveContainers(ctx, p)
		if err != nil {
			return err
		}
		routes, err = translate.CollectRoutes(gatewayServices(p), live)
		if err != nil {
			return err
		}
		if len(routes) == 0 {
			a.infof("WARN host-gateway: no running published endpoints to program; skipping session rules")
			return nil
		}
	}
	script, err := translate.GatewayScript(p.Name, routes, cleanup)
	if err != nil {
		return err
	}
	if err := a.c.Session(ctx, script, translate.GatewayStatusPath(p.Name)); err != nil {
		return fmt.Errorf("host-gateway: %w", err)
	}
	if cleanup || a.c.DryRun() {
		return nil
	}
	// Remember that rules were programmed so a later down can remove them
	// even after the labeled containers are already gone.
	if err := a.noteGatewayProgrammed(p.Name); err != nil {
		a.infof("WARN host-gateway: could not record programmed rules: %v", err)
	}
	return nil
}

func (a *App) gatewayProgrammed(project string) (bool, error) {
	dir, err := a.projectState(project)
	if err != nil {
		return false, err
	}
	return state.GatewayMarked(dir)
}

func (a *App) noteGatewayProgrammed(project string) error {
	dir, err := a.projectState(project)
	if err != nil {
		return err
	}
	return state.MarkGateway(dir)
}

func projectUsesGateway(p *types.Project) bool {
	for _, s := range p.Services {
		if translate.UsesHostGateway(s.ExtraHosts) {
			return true
		}
	}
	return false
}

func gatewayServices(p *types.Project) []translate.GatewayService {
	out := make([]translate.GatewayService, 0, len(p.Services))
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		gs := translate.GatewayService{
			Name:    name,
			Ports:   s.Ports,
			Gateway: translate.UsesHostGateway(s.ExtraHosts),
		}
		if s.NetworkMode == "" {
			for _, key := range s.NetworksByPriority() {
				n := p.Networks[key]
				if n.Name != "" {
					gs.Networks = append(gs.Networks, n.Name)
				} else {
					gs.Networks = append(gs.Networks, key)
				}
			}
		}
		out = append(out, gs)
	}
	return out
}

func (a *App) liveContainers(ctx context.Context, p *types.Project) ([]translate.LiveContainer, error) {
	cs, err := a.c.Containers(ctx, p.Name, true)
	if err != nil {
		return nil, err
	}
	var out []translate.LiveContainer
	for _, c := range cs {
		raw, err := a.c.InspectRaw(ctx, firstNonEmpty(c.ID, c.Name))
		if err != nil {
			return nil, err
		}
		rt := wslc.ParseRuntime(raw)
		live := translate.LiveContainer{
			Name: c.Name, Service: c.Service(), Running: c.Running(), Primary: rt.Primary,
		}
		for _, ep := range rt.Endpoints {
			live.Endpoints = append(live.Endpoints, translate.LiveEndpoint{Network: ep.Network, IP: ep.IP, Gateway: ep.Gateway})
		}
		for _, port := range rt.Ports {
			live.Ports = append(live.Ports, translate.LivePort{Target: port.Target, Proto: port.Proto, HostPort: port.HostPort, HostIP: port.HostIP})
		}
		out = append(out, live)
	}
	return out, nil
}

// sawHostGateway reports whether a container that down is removing was
// labeled as part of a host-gateway project. The compose file asking for
// host-gateway is not enough: rules may never have been programmed.
func sawHostGateway(cs []wslc.Container) bool {
	for _, c := range cs {
		if c.Labels[labels.HostGateway] == "v1" {
			return true
		}
	}
	return false
}
