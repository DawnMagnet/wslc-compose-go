package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/dawnmagnet/wslc-compose-go/internal/translate"
	"github.com/dawnmagnet/wslc-compose-go/internal/wslc"
)

type imageOptions struct {
	build, noBuild, noCache, pullOnBuild bool
	pull                                 string // pull_policy override
}

// prepareImages builds or pulls images according to pull_policy and flags.
// It returns the services whose image was (re)built, which must be
// recreated even if their configuration hash is unchanged.
func (a *App) prepareImages(ctx context.Context, p *types.Project, o imageOptions) (map[string]bool, error) {
	rebuilt := map[string]bool{}
	pulled := map[string]bool{}
	t := a.translator(p)
	stdio := wslc.IO{Stdout: a.io.Stderr, Stderr: a.io.Stderr}
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		img := translate.ImageName(p, s)
		policy := s.PullPolicy
		if o.pull != "" {
			policy = o.pull
		}
		if s.Build != nil && !o.noBuild {
			if o.build || policy == types.PullPolicyBuild || !a.c.ImageExists(ctx, img) {
				a.infof("Image %s  Building (service %s)", img, name)
				if err := a.c.Build(ctx, t.Build(s, o.noCache, o.pullOnBuild), stdio); err != nil {
					return nil, fmt.Errorf("build %s: %w", name, err)
				}
				rebuilt[name] = true
			}
			continue
		}
		if s.Image == "" || pulled[img] {
			continue
		}
		switch policy {
		case types.PullPolicyNever, types.PullPolicyBuild:
			continue
		case types.PullPolicyAlways:
		default: // missing / if_not_present / refresh / daily ...
			if a.c.ImageExists(ctx, img) {
				continue
			}
		}
		a.infof("Image %s  Pulling", img)
		if err := a.c.Pull(ctx, img, stdio); err != nil {
			return nil, err // already names the image
		}
		pulled[img] = true
	}
	return rebuilt, nil
}

// BuildOptions are the flags of `build`.
type BuildOptions struct {
	Services []string
	NoCache  bool
	Pull     bool
}

// Build builds every selected service that has a build section.
func (a *App) Build(ctx context.Context, o BuildOptions) error {
	p, err := a.load(ctx, o.Services, true, false)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(p.ServiceNames(), func(n string) bool { return p.Services[n].Build != nil }) {
		a.infof("No services to build")
		return nil
	}
	_, err = a.prepareImages(ctx, p, imageOptions{build: true, noCache: o.NoCache, pullOnBuild: o.Pull, pull: types.PullPolicyNever})
	return err
}

// PullOptions are the flags of `pull`.
type PullOptions struct {
	Services       []string
	IgnoreFailures bool
}

// Pull pulls the image of every selected service that declares one.
// Services that can also build tolerate pull failures.
func (a *App) Pull(ctx context.Context, o PullOptions) error {
	p, err := a.load(ctx, o.Services, true, false)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	stdio := wslc.IO{Stdout: a.io.Stderr, Stderr: a.io.Stderr}
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		if s.Image == "" || seen[s.Image] {
			continue
		}
		seen[s.Image] = true
		a.infof("Image %s  Pulling", s.Image)
		if err := a.c.Pull(ctx, s.Image, stdio); err != nil {
			if o.IgnoreFailures || s.Build != nil {
				a.infof("WARN pull %s: %v", s.Image, err)
				continue
			}
			return err
		}
	}
	return nil
}
