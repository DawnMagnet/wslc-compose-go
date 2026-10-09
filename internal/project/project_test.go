package project

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dawnmagnet/wslc-compose-go/internal/golden"
)

func load(t *testing.T, file string, mod func(*Options)) *Options {
	t.Helper()
	o := &Options{Files: []string{golden.Compose(file)}, IgnoreOSEnv: true}
	if mod != nil {
		mod(o)
	}
	return o
}

func TestLoadFull(t *testing.T) {
	p, err := Load(context.Background(), *load(t, "full.yaml", nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "full" {
		t.Fatalf("name = %q", p.Name)
	}
	if got := strings.Join(p.ServiceNames(), ","); got != "api,db,web" {
		t.Fatalf("services = %s", got)
	}
	api := p.Services["api"]
	if v := api.Environment["API_KEY"]; v == nil || *v != "from-env-file" {
		t.Fatalf("env_file not merged: %v", api.Environment)
	}
	if api.GetScale() != 2 {
		t.Fatalf("replicas = %d", api.GetScale())
	}
	if p.Volumes["dbdata"].Name != "shared-dbdata" || p.Networks["front"].Name != "full_front" {
		t.Fatalf("resource names not normalized: %+v %+v", p.Volumes, p.Networks)
	}
}

func TestLoadSelection(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		mod  func(*Options)
		want string
	}{
		{"default profiles", nil, "app,migrate"},
		{"profile enabled", func(o *Options) { o.Profiles = []string{"debug"} }, "app,debug,migrate"},
		{"COMPOSE_PROFILES", func(o *Options) { o.Env = []string{"COMPOSE_PROFILES=debug"} }, "app,debug,migrate"},
		{"service with deps", func(o *Options) { o.Services = []string{"app"} }, "app,migrate"},
		{"service no deps", func(o *Options) { o.Services = []string{"app"}; o.NoDeps = true }, "app"},
		{"project name override", func(o *Options) { o.Name = "custom" }, "app,migrate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Load(ctx, *load(t, "profiles.yaml", tt.mod))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(p.ServiceNames(), ","); got != tt.want {
				t.Fatalf("services = %s, want %s", got, tt.want)
			}
		})
	}
	p, _ := Load(ctx, *load(t, "profiles.yaml", func(o *Options) { o.Name = "custom" }))
	if p.Name != "custom" {
		t.Fatalf("name override ignored: %s", p.Name)
	}
	if _, err := Load(ctx, *load(t, "profiles.yaml", func(o *Options) { o.Services = []string{"nope"} })); err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func TestInterpolation(t *testing.T) {
	p, err := Load(context.Background(), Options{
		Files: []string{golden.Compose("minimal.yaml")}, IgnoreOSEnv: true, Env: []string{"UNUSED=1"},
	})
	if err != nil || p.Services["hello"].Image != "nginx:alpine" {
		t.Fatalf("load minimal: %v", err)
	}
}

func TestPolicy(t *testing.T) {
	ctx := context.Background()
	p, err := Load(ctx, *load(t, "unsupported.yaml", nil))
	if err != nil {
		t.Fatal(err)
	}
	issues := Check(p)
	fields := map[string]bool{}
	for _, i := range issues {
		fields[i.Field] = true
	}
	for _, f := range []string{"restart", "privileged", "cap_add", "devices", "security_opt", "sysctls",
		"extra_hosts", "secrets", "logging", "platform", "network_mode"} {
		if !fields[f] {
			t.Errorf("missing issue for %s", f)
		}
	}
	var buf bytes.Buffer
	if err := Enforce(issues, false, &buf); err != nil {
		t.Fatalf("non-strict mode must not fail: %v", err)
	}
	if !strings.Contains(buf.String(), "WARN [svc] privileged") {
		t.Fatalf("warnings not printed: %s", buf.String())
	}
	if err := Enforce(issues, true, &bytes.Buffer{}); !errors.Is(err, ErrStrict) {
		t.Fatalf("strict mode: got %v", err)
	}

	clean, _ := Load(ctx, *load(t, "minimal.yaml", nil))
	if got := Check(clean); len(got) != 0 {
		t.Fatalf("minimal project should be clean, got %v", got)
	}
	// Info-level notes never fail strict mode.
	full, _ := Load(ctx, *load(t, "full.yaml", func(o *Options) { o.Services = []string{"db"} }))
	if err := Enforce(Check(full), true, &bytes.Buffer{}); err != nil {
		t.Fatalf("db service should pass strict: %v", err)
	}
}

func TestServiceHash(t *testing.T) {
	p, err := Load(context.Background(), *load(t, "full.yaml", nil))
	if err != nil {
		t.Fatal(err)
	}
	base := p.Services["api"]
	h0, _ := ServiceHash(base)
	if h1, _ := ServiceHash(base); h0 != h1 || len(h0) != 64 {
		t.Fatalf("hash not stable: %s %s", h0, h1)
	}
	changed := base
	changed.Image = "example/api:2.0"
	if h, _ := ServiceHash(changed); h == h0 {
		t.Fatal("image change must change the hash")
	}
	env := base
	v := "x"
	env.Environment = map[string]*string{"NEW": &v}
	if h, _ := ServiceHash(env); h == h0 {
		t.Fatal("environment change must change the hash")
	}
	scaled := base
	n := 7
	scaled.Scale = &n
	deploy := *base.Deploy
	deploy.Replicas = &n
	scaled.Deploy = &deploy
	scaled.PullPolicy = "always"
	scaled.DependsOn = nil
	if h, _ := ServiceHash(scaled); h != h0 {
		t.Fatal("scale/pull_policy/depends_on must not change the hash")
	}
	if *base.Deploy.Replicas != 2 {
		t.Fatal("ServiceHash must not mutate its input")
	}
}
