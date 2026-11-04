package state

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestPrepareGeneratedAndExternal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	app := types.ServiceConfig{Name: "app"}
	app.Secrets = []types.ServiceSecretConfig{
		types.ServiceSecretConfig(types.FileReferenceConfig{Source: "from_env", Target: "from_env", Mode: mode(0o440)}),
		types.ServiceSecretConfig(types.FileReferenceConfig{Source: "outside"}),
	}
	app.Configs = []types.ServiceConfigObjConfig{
		types.ServiceConfigObjConfig(types.FileReferenceConfig{Source: "inline", Target: "/etc/app.cfg"}),
	}
	p := &types.Project{
		Name:     "p",
		Services: types.Services{"app": app},
		Secrets: types.Secrets{
			"from_env": {Environment: "API_KEY", Content: "s3cret-value"},
			"outside":  {External: true},
		},
		Configs: types.Configs{
			"inline": {Content: "hello-config\n"},
		},
	}
	got, created, err := Prepare(dir, p, true)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first Prepare should report a new state dir")
	}
	if _, ok := got.SecretFiles["outside"]; ok {
		t.Fatal("external secret must not be materialised")
	}
	body, err := os.ReadFile(got.SecretFiles["from_env"])
	if err != nil || string(body) != "s3cret-value" {
		t.Fatalf("env secret: %q %v", body, err)
	}
	info, err := os.Stat(got.SecretFiles["from_env"])
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if info.Mode().Perm()&0o222 != 0 {
			t.Fatalf("secret should not be writable, mode %v", info.Mode())
		}
	} else if info.Mode().Perm() != 0o440 {
		t.Fatalf("mode: %v", info.Mode())
	}
	if runtime.GOOS != "windows" {
		dirInfo, err := os.Stat(dir)
		if err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("state dir mode: %v %v", dirInfo.Mode(), err)
		}
	}
	mounts := got.Mounts["app"]
	if len(mounts) != 2 || mounts[0].Target != "/run/secrets/from_env" || mounts[1].Target != "/etc/app.cfg" {
		t.Fatalf("mounts: %+v", mounts)
	}
	cfg, err := os.ReadFile(mounts[1].Source)
	if err != nil || string(cfg) != "hello-config\n" {
		t.Fatalf("config: %q %v", cfg, err)
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("state dir still present: %v", err)
	}
}

func TestGatewayMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	on, err := GatewayMarked(dir)
	if err != nil || on {
		t.Fatalf("missing dir: %v %v", on, err)
	}
	if err := MarkGateway(dir); err != nil {
		t.Fatal(err)
	}
	on, err = GatewayMarked(dir)
	if err != nil || !on {
		t.Fatalf("marked: %v %v", on, err)
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	on, err = GatewayMarked(dir)
	if err != nil || on {
		t.Fatalf("removed marker: %v %v", on, err)
	}
}

func TestPrepareMissingFile(t *testing.T) {
	app := types.ServiceConfig{Name: "app"}
	app.Secrets = []types.ServiceSecretConfig{types.ServiceSecretConfig(types.FileReferenceConfig{Source: "tok"})}
	p := &types.Project{
		Services: types.Services{"app": app},
		Secrets:  types.Secrets{"tok": {File: filepath.Join(t.TempDir(), "nope")}},
	}
	if _, _, err := Prepare(t.TempDir(), p, true); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing file: %v", err)
	}
	empty := types.ServiceConfig{Name: "app"}
	empty.Secrets = []types.ServiceSecretConfig{types.ServiceSecretConfig(types.FileReferenceConfig{Source: "e"})}
	p.Services["app"] = empty
	p.Secrets = types.Secrets{"e": {Environment: "MISSING"}}
	if _, _, err := Prepare(t.TempDir(), p, true); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty env: %v", err)
	}
}

func TestPrepareTwiceAndSharedSecret(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) types.ServiceConfig {
		s := types.ServiceConfig{Name: name}
		s.Secrets = []types.ServiceSecretConfig{
			types.ServiceSecretConfig(types.FileReferenceConfig{Source: "shared", Mode: mode(0o440)}),
		}
		s.Build = &types.BuildConfig{Context: ".", Secrets: []types.ServiceSecretConfig{
			types.ServiceSecretConfig(types.FileReferenceConfig{Source: "shared"}),
		}}
		return s
	}
	a, b := mk("a"), mk("b")
	p := &types.Project{
		Services: types.Services{"a": a, "b": b},
		Secrets:  types.Secrets{"shared": {Environment: "TOK", Content: "once"}},
	}
	first, _, err := Prepare(dir, p, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mounts["a"][0].Source != first.Mounts["b"][0].Source || first.SecretFiles["shared"] != first.Mounts["a"][0].Source {
		t.Fatalf("services must share one file: %+v", first)
	}
	second, created, err := Prepare(dir, p, true)
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if created {
		t.Fatal("second Prepare must not treat an existing dir as new")
	}
	body, err := os.ReadFile(second.SecretFiles["shared"])
	if err != nil || string(body) != "once" {
		t.Fatalf("rewritten secret: %q %v", body, err)
	}
	planned, _, err := Prepare(dir, p, false)
	if err != nil {
		t.Fatal(err)
	}
	if planned.SecretFiles["shared"] != first.SecretFiles["shared"] {
		t.Fatalf("dry-run path %s, want %s", planned.SecretFiles["shared"], first.SecretFiles["shared"])
	}
}

func TestPrepareRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege varies")
	}
	dir := t.TempDir()
	real := filepath.Join(t.TempDir(), "real")
	if err := os.WriteFile(real, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(linkDir, "from_env")); err != nil {
		t.Fatal(err)
	}
	app := types.ServiceConfig{Name: "app"}
	app.Secrets = []types.ServiceSecretConfig{
		types.ServiceSecretConfig(types.FileReferenceConfig{Source: "from_env"}),
	}
	p := &types.Project{
		Services: types.Services{"app": app},
		Secrets:  types.Secrets{"from_env": {Content: "replaced"}},
	}
	if _, _, err := Prepare(dir, p, true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink: %v", err)
	}
	body, err := os.ReadFile(real)
	if err != nil || string(body) != "keep" {
		t.Fatalf("symlink target changed: %q %v", body, err)
	}
}

func mode(m types.FileMode) *types.FileMode { return &m }
