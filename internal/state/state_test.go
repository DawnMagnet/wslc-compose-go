package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestPrepareGeneratedAndExternal(t *testing.T) {
	dir := t.TempDir()
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
	got, err := Prepare(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.SecretFiles["outside"]; ok {
		t.Fatal("external secret must not be materialised")
	}
	body, err := os.ReadFile(got.SecretFiles["from_env"])
	if err != nil || string(body) != "s3cret-value" {
		t.Fatalf("env secret: %q %v", body, err)
	}
	info, err := os.Stat(got.SecretFiles["from_env"])
	if err != nil || info.Mode().Perm() != 0o440 {
		t.Fatalf("mode: %v %v", info, err)
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

func TestPrepareMissingFile(t *testing.T) {
	app := types.ServiceConfig{Name: "app"}
	app.Secrets = []types.ServiceSecretConfig{types.ServiceSecretConfig(types.FileReferenceConfig{Source: "tok"})}
	p := &types.Project{
		Services: types.Services{"app": app},
		Secrets:  types.Secrets{"tok": {File: filepath.Join(t.TempDir(), "nope")}},
	}
	if _, err := Prepare(t.TempDir(), p); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing file: %v", err)
	}
	empty := types.ServiceConfig{Name: "app"}
	empty.Secrets = []types.ServiceSecretConfig{types.ServiceSecretConfig(types.FileReferenceConfig{Source: "e"})}
	p.Services["app"] = empty
	p.Secrets = types.Secrets{"e": {Environment: "MISSING"}}
	if _, err := Prepare(t.TempDir(), p); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty env: %v", err)
	}
}

func mode(m types.FileMode) *types.FileMode { return &m }
