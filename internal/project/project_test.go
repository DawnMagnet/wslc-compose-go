package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/golden"
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
		"extra_hosts", "logging", "platform", "network_mode"} {
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

func TestNetworkPolicy(t *testing.T) {
	p, err := Load(context.Background(), *load(t, "networks.yaml", nil))
	if err != nil {
		t.Fatal(err)
	}
	if issues := Check(p); len(issues) != 0 {
		t.Fatalf("current wslc should accept networks.yaml, got %v", issues)
	}
	old := CheckEngine(p, Engine{})
	fields := map[string]bool{}
	for _, i := range old {
		fields[i.Field] = true
	}
	for _, f := range []string{"networks", "networks.back.ipv4_address", "networks.back.ipam.ip_range", "networks.front.link_local_ips"} {
		if !fields[f] {
			t.Errorf("old engine missing %s in %v", f, old)
		}
	}
	v6 := p.Services["app"]
	v6.Networks["back"].Ipv6Address = "fd00::10"
	p.Services["app"] = v6
	if issues := Check(p); !hasField(issues, "networks.back.ipv6_address") {
		t.Fatalf("ipv6 must warn on a current engine: %v", issues)
	}
}

func hasField(issues []Issue, field string) bool {
	for _, i := range issues {
		if i.Field == field {
			return true
		}
	}
	return false
}

func TestServiceHash(t *testing.T) {
	p, err := Load(context.Background(), *load(t, "full.yaml", nil))
	if err != nil {
		t.Fatal(err)
	}
	NameAnonymousVolumes(p)
	base := p.Services["api"]
	h0, _ := ServiceHash(p, base, LatestEngine())
	if h1, _ := ServiceHash(p, base, LatestEngine()); h0 != h1 || len(h0) != 64 {
		t.Fatalf("hash not stable: %s %s", h0, h1)
	}
	if legacy, _ := ServiceHash(p, base, Engine{}); legacy == h0 {
		t.Fatal("multi-network on a current wslc must change the hash")
	}
	// On wslc 3.0.1 the api command is still the v0.1.5 `run` line.
	if old, _ := ServiceHash(p, base, Engine{}); old != legacyHash(base) {
		t.Fatalf("legacy engine hash %s, v0.1.5 %s", old, legacyHash(base))
	}
	changed := base
	changed.Image = "example/api:2.0"
	if h, _ := ServiceHash(p, changed, LatestEngine()); h == h0 {
		t.Fatal("image change must change the hash")
	}
	env := base
	v := "x"
	env.Environment = map[string]*string{"NEW": &v}
	if h, _ := ServiceHash(p, env, LatestEngine()); h == h0 {
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
	if h, _ := ServiceHash(p, scaled, LatestEngine()); h != h0 {
		t.Fatal("scale/pull_policy/depends_on must not change the hash")
	}
	if *base.Deploy.Replicas != 2 {
		t.Fatal("ServiceHash must not mutate its input")
	}

	mini, err := Load(context.Background(), *load(t, "minimal.yaml", nil))
	if err != nil {
		t.Fatal(err)
	}
	NameAnonymousVolumes(mini)
	hello := mini.Services["hello"]
	const want = "0df62ea0f7a4228c79ebe55b0d198f8ce5de02a73ed48a3d6d6d15a7257493bc"
	for _, eng := range []Engine{{}, LatestEngine()} {
		got, err := ServiceHash(mini, hello, eng)
		if err != nil || got != want {
			t.Fatalf("engine %+v hash %s, want %s (%v)", eng, got, want, err)
		}
	}
	db := p.Services["db"]
	if got, _ := ServiceHash(p, db, LatestEngine()); got != legacyHash(db) {
		t.Fatalf("single-network db hash changed: %s", got)
	}

	ext := types.ServiceConfig{Name: "ext"}
	ext.Secrets = []types.ServiceSecretConfig{types.ServiceSecretConfig(types.FileReferenceConfig{Source: "out"})}
	ep := &types.Project{Services: types.Services{"ext": ext}, Secrets: types.Secrets{"out": {External: true}}}
	if got, _ := ServiceHash(ep, ext, LatestEngine()); got != legacyHash(ext) {
		t.Fatal("external secret must not change the hash")
	}
	ep.Secrets["out"] = types.SecretConfig{File: "secret.txt"}
	if got, _ := ServiceHash(ep, ext, LatestEngine()); got == legacyHash(ext) {
		t.Fatal("file secret must change the hash")
	}
	cfg := types.ServiceConfig{Name: "cfg"}
	cfg.Configs = []types.ServiceConfigObjConfig{types.ServiceConfigObjConfig(types.FileReferenceConfig{Source: "out"})}
	cp := &types.Project{Services: types.Services{"cfg": cfg}, Configs: types.Configs{"out": {External: true}}}
	if got, _ := ServiceHash(cp, cfg, LatestEngine()); got != legacyHash(cfg) {
		t.Fatal("external config must not change the hash")
	}
	cp.Configs["out"] = types.ConfigObjConfig{File: "cfg.txt"}
	if got, _ := ServiceHash(cp, cfg, LatestEngine()); got == legacyHash(cfg) {
		t.Fatal("file config must change the hash")
	}
	ranged := types.ServiceConfig{Name: "ranged"}
	ranged.Networks = map[string]*types.ServiceNetworkConfig{"pool": {}}
	rp := &types.Project{
		Services: types.Services{"ranged": ranged},
		Networks: types.Networks{"pool": {Ipam: types.IPAMConfig{Config: []*types.IPAMPool{{Subnet: "10.2.0.0/24", IPRange: "10.2.0.128/25"}}}}},
	}
	if got, _ := ServiceHash(rp, ranged, LatestEngine()); got != legacyHash(ranged) {
		t.Fatal("ip_range must not change the container hash")
	}
	built := types.ServiceConfig{Name: "built"}
	built.Build = &types.BuildConfig{Context: ".", Secrets: []types.ServiceSecretConfig{
		types.ServiceSecretConfig(types.FileReferenceConfig{Source: "tok"}),
	}}
	bp := &types.Project{Services: types.Services{"built": built}, Secrets: types.Secrets{"tok": {File: "tok"}}}
	if got, _ := ServiceHash(bp, built, LatestEngine()); got != legacyHash(built) {
		t.Fatal("build.secrets must not change the container hash")
	}
}

func legacyHash(s types.ServiceConfig) string {
	s = stripHash(s)
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestNameAnonymousVolumes(t *testing.T) {
	p, err := Load(context.Background(), *load(t, "full.yaml", nil))
	if err != nil {
		t.Fatal(err)
	}
	NameAnonymousVolumes(p)
	NameAnonymousVolumes(p) // idempotent
	var got []string
	for _, v := range p.Services["web"].Volumes {
		if v.Target == "/scratch" {
			got = append(got, v.Source)
		}
	}
	if len(got) != 1 || got[0] != "web_scratch_anon" {
		t.Fatalf("anonymous volume not named: %v", got)
	}
	if v := p.Volumes["web_scratch_anon"]; v.Name != "full_web_scratch_anon" {
		t.Fatalf("volume not declared: %+v", v)
	}
	for in, want := range map[string]string{"/var/lib/Data-1": "var_lib_data_1", "/": "root", "/a//b/": "a_b"} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
