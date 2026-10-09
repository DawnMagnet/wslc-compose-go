package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/project"
	"github.com/DawnMagnet/wslc-compose-go/internal/translate"
)

// ExecOptions are the flags of `exec`.
type ExecOptions struct {
	Service     string
	Index       int
	Command     []string
	Interactive bool
	TTY         bool
	Detach      bool
	User        string
	Workdir     string
	Env         []string
}

// Exec runs a command in a running service container.
func (a *App) Exec(ctx context.Context, o ExecOptions) error {
	if len(o.Command) == 0 {
		return errors.New("exec requires a command")
	}
	p, err := a.load(ctx, nil, false, true)
	if err != nil {
		return err
	}
	s, err := p.GetService(o.Service)
	if err != nil {
		return err
	}
	idx := max(o.Index, 1)
	args := []string{"exec"}
	if o.Detach {
		args = append(args, "-d")
	}
	if o.Interactive {
		args = append(args, "-i")
	}
	if o.TTY {
		args = append(args, "-t")
	}
	if o.User != "" {
		args = append(args, "-u", o.User)
	}
	if o.Workdir != "" {
		args = append(args, "-w", o.Workdir)
	}
	for _, e := range o.Env {
		args = append(args, "-e", e)
	}
	args = append(args, translate.ContainerName(p, s, idx))
	return a.c.Exec(ctx, append(args, o.Command...), a.io)
}

// RunOptions are the flags of `run`.
type RunOptions struct {
	Service      string
	Command      []string
	Name         string
	Remove       bool
	Detach       bool
	NoDeps       bool
	ServicePorts bool
	NoTTY        bool
	User         string
	Workdir      string
	Entrypoint   []string
	Env          []string
	Build        bool
}

// Run starts a one-off container for a service, bringing up its
// dependencies first unless NoDeps is set.
func (a *App) Run(ctx context.Context, o RunOptions) error {
	p, err := a.load(ctx, []string{o.Service}, o.NoDeps, false)
	if err != nil {
		return err
	}
	s, err := p.GetService(o.Service)
	if err != nil {
		return err
	}
	if deps := s.GetDependencies(); len(deps) > 0 && !o.NoDeps {
		if err := a.Up(ctx, UpOptions{Services: deps, Detach: true, Build: o.Build, Timeout: -1}); err != nil {
			return err
		}
	} else if err := a.ensureResources(ctx, p, nil); err != nil {
		return err
	}
	only, err := p.WithSelectedServices([]string{o.Service}, types.IgnoreDependencies)
	if err != nil {
		return err
	}
	if _, err := a.prepareImages(ctx, only, imageOptions{build: o.Build}); err != nil {
		return err
	}
	if o.User != "" {
		s.User = o.User
	}
	if o.Workdir != "" {
		s.WorkingDir = o.Workdir
	}
	if o.Entrypoint != nil {
		s.Entrypoint = o.Entrypoint
	}
	hash, err := project.ServiceHash(s)
	if err != nil {
		return err
	}
	name := o.Name
	if name == "" {
		name = fmt.Sprintf("%s-%s-run-%s", p.Name, s.Name, randomSuffix())
	}
	var cmd []string
	if len(o.Command) > 0 {
		cmd = o.Command
	}
	args := a.translator(p).Run(s, translate.RunOptions{
		Name: name, Hash: hash, Detach: o.Detach, Remove: o.Remove,
		Command: cmd, NoTTY: o.NoTTY, TTY: !o.NoTTY && !o.Detach,
		Publish: o.ServicePorts, ExtraEnv: o.Env,
	})
	if o.Detach {
		return a.c.Run(ctx, args)
	}
	return a.c.RunAttached(ctx, args, a.io)
}

func randomSuffix() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return strconv.Itoa(int(b[0]))
	}
	return hex.EncodeToString(b)
}

func imageOf(p *types.Project, service string) string {
	return translate.ImageName(p, p.Services[service])
}
