// Package project loads Compose files through the reference compose-go
// loader and applies the wslc-specific policy on top of the normalized model.
package project

import (
	"context"
	"os"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
)

// Options selects which Compose model to load and which services to keep.
type Options struct {
	Files       []string // -f; empty means compose.yaml discovery / COMPOSE_FILE
	Name        string   // -p; empty means `name:` or the directory name
	WorkDir     string   // --project-directory
	EnvFiles    []string // --env-file
	Profiles    []string // --profile; empty falls back to COMPOSE_PROFILES
	Services    []string // explicit service selection; empty means all enabled
	NoDeps      bool     // do not pull in dependencies of selected services
	Env         []string // extra KEY=VALUE pairs, mainly for tests
	IgnoreOSEnv bool     // skip the process environment (hermetic tests)
}

// Load parses, interpolates, merges and normalizes the Compose model, then
// narrows it to the selected services (plus dependencies unless NoDeps).
func Load(ctx context.Context, o Options) (*types.Project, error) {
	fns := []cli.ProjectOptionsFn{cli.WithWorkingDirectory(o.WorkDir)}
	if !o.IgnoreOSEnv {
		fns = append(fns, cli.WithOsEnv)
	}
	fns = append(fns,
		cli.WithEnv(o.Env),
		cli.WithEnvFiles(o.EnvFiles...),
		cli.WithDotEnv,
		cli.WithConfigFileEnv,
		cli.WithDefaultConfigPath,
	)
	if o.Name != "" {
		fns = append(fns, cli.WithName(o.Name))
	}
	fns = append(fns, cli.WithProfiles(profiles(o)))

	opts, err := cli.NewProjectOptions(o.Files, fns...)
	if err != nil {
		return nil, err
	}
	p, err := opts.LoadProject(ctx)
	if err != nil {
		return nil, err
	}
	dep := types.IncludeDependencies
	if o.NoDeps {
		dep = types.IgnoreDependencies
	}
	return p.WithSelectedServices(o.Services, dep)
}

func profiles(o Options) []string {
	if len(o.Profiles) > 0 {
		return o.Profiles
	}
	v := os.Getenv("COMPOSE_PROFILES")
	for _, kv := range o.Env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == "COMPOSE_PROFILES" {
			v = val
		}
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
