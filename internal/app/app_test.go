package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DawnMagnet/wslc-compose-go/internal/golden"
	"github.com/DawnMagnet/wslc-compose-go/internal/project"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

var ctx = context.Background()

func TestUpFreshGolden(t *testing.T) {
	for _, file := range []string{"full.yaml", "minimal.yaml", "profiles.yaml"} {
		t.Run(file, func(t *testing.T) {
			h := newHarness(t, file, nil)
			h.fake.health["full-api-1"] = []string{"starting", "healthy"}
			h.fake.health["full-api-2"] = []string{"healthy"}
			h.fake.images["busybox"] = true
			h.fake.exited["profiles-migrate-1"] = 0
			if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1, WaitTimeout: 5 * time.Second}); err != nil {
				t.Fatalf("up: %v\n%s", err, h.stderr.String())
			}
			golden.Assert(t, "up_"+strings.TrimSuffix(file, ".yaml")+".golden", h.fake.mutations())
		})
	}
}

func TestUpNetworksGolden(t *testing.T) {
	h := newHarness(t, "networks.yaml", nil)
	h.fake.networks = []string{"already"}
	h.fake.images["alpine"] = true
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("up: %v\n%s", err, h.stderr.String())
	}
	golden.Assert(t, "up_networks.golden", h.fake.mutations())
	dry := newHarness(t, "networks.yaml", func(o *Options) { o.DryRun = true })
	dry.fake.networks = []string{"already"}
	dry.fake.images["alpine"] = true
	if err := dry.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "dryrun_networks.golden", hashRE.ReplaceAllString(dry.stdout.String(), "config-hash=<hash>"))
}

func TestUpNetworksDegradeOnOldWslc(t *testing.T) {
	h := newHarness(t, "networks.yaml", nil)
	h.fake.version = "wslc version 3.0.1.0"
	h.fake.networks = []string{"already"}
	h.fake.images["alpine"] = true
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("up: %v\n%s", err, h.stderr.String())
	}
	m := h.fake.mutations()
	if strings.Contains(m, "network connect") || strings.Contains(m, "--ip") || strings.Contains(m, "--ip-range") {
		t.Fatalf("old wslc must not be sent gated flags:\n%s", m)
	}
	if !strings.Contains(m, "wslc run -d") || !strings.Contains(m, "--network nets_back") {
		t.Fatalf("primary network should still be attached:\n%s", m)
	}
	if !strings.Contains(h.stderr.String(), "one network") || !strings.Contains(h.stderr.String(), "no --ip") {
		t.Fatalf("missing degradation warnings:\n%s", h.stderr.String())
	}
	h.fake.calls = nil
	h.app = New(Options{
		Files: h.app.o.Files, IgnoreOSEnv: true, Bin: "wslc", Strict: true,
		DefaultDNS: []string{"1.1.1.1"}, PollEvery: time.Millisecond,
	}, h.fake, golden.Paths, wslc.IO{Stdout: &h.stdout, Stderr: &h.stderr})
	if err := h.app.Up(ctx, UpOptions{Detach: true}); !errors.Is(err, project.ErrStrict) {
		t.Fatalf("strict + old wslc: %v", err)
	}
}

func TestUpSecretsGolden(t *testing.T) {
	h := newHarness(t, "secrets.yaml", func(o *Options) {
		o.Env = []string{"API_KEY=s3cret-value"}
	})
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("up: %v\n%s", err, h.stderr.String())
	}
	m := h.fake.mutations()
	if strings.Contains(m, "s3cret-value") || strings.Contains(m, "hello-config") {
		t.Fatalf("secret bytes leaked onto the command line:\n%s", m)
	}
	golden.Assert(t, "up_secrets.golden", m)
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("second up without down: %v\n%s", err, h.stderr.String())
	}
	bodyAgain, err := os.ReadFile(filepath.Join(h.app.o.StateDir, "sec", "secrets", "api_key"))
	if err != nil || string(bodyAgain) != "s3cret-value" {
		t.Fatalf("secret after second up: %q %v", bodyAgain, err)
	}
	if !strings.Contains(h.stderr.String(), "external secret") {
		t.Fatalf("external secret warning missing:\n%s", h.stderr.String())
	}
	body, err := os.ReadFile(filepath.Join(h.app.o.StateDir, "sec", "secrets", "api_key"))
	if err != nil || string(body) != "s3cret-value" {
		t.Fatalf("generated secret: %q %v", body, err)
	}
	if err := h.app.Down(ctx, DownOptions{Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.app.o.StateDir, "sec")); !os.IsNotExist(err) {
		t.Fatalf("state dir not removed: %v", err)
	}

	h = newHarness(t, "secrets.yaml", func(o *Options) {
		o.Env = []string{"API_KEY=s3cret-value"}
	})
	h.fake.version = "wslc version 3.0.1.0"
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("old wslc: %v\n%s", err, h.stderr.String())
	}
	old := h.fake.mutations()
	if strings.Contains(old, "--secret") {
		t.Fatalf("old wslc was sent build --secret:\n%s", old)
	}
	if !strings.Contains(old, ":/run/secrets/token:ro") || !strings.Contains(h.stderr.String(), "build --secret") {
		t.Fatalf("runtime mounts should remain while build secrets warn:\n%s\n%s", old, h.stderr.String())
	}
	dry := newHarness(t, "secrets.yaml", func(o *Options) {
		o.Env = []string{"API_KEY=s3cret-value"}
		o.DryRun = true
	})
	if err := dry.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "dryrun_secrets.golden", hashRE.ReplaceAllString(dry.stdout.String(), "config-hash=<hash>"))
	if _, err := os.Stat(filepath.Join(dry.app.o.StateDir, "sec")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write the state dir: %v", err)
	}
}

func TestUpHostsGolden(t *testing.T) {
	h := newHarness(t, "hosts.yaml", nil)
	h.fake.track = true
	h.fake.images["alpine"] = true
	h.fake.runtime = map[string]map[string]any{
		"hosts-web-1": {
			"HostConfig": map[string]any{"NetworkMode": "hosts_default"},
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{
					"hosts_default": map[string]any{"IPAddress": "172.28.0.2", "Gateway": "172.28.0.1"},
				},
			},
			"Ports": map[string]any{
				"80/tcp": []any{map[string]any{"HostIp": "0.0.0.0", "HostPort": "8080"}},
			},
		},
	}
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("up: %v\n%s", err, h.stderr.String())
	}
	m := h.fake.mutations()
	if !strings.Contains(m, "exec -u 0 hosts-web-1") || !strings.Contains(m, "system session run") || !strings.Contains(m, "DNAT") {
		t.Fatalf("missing extra_hosts or host-gateway rules:\n%s", m)
	}
	if strings.Contains(m, "exec -u 0 hosts-db-1") {
		t.Fatal("db has no extra_hosts")
	}
	golden.Assert(t, "up_hosts.golden", m)
	h.fake.calls = nil
	if err := h.app.Down(ctx, DownOptions{Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	down := h.fake.mutations()
	if !strings.Contains(down, "system session run") || strings.Contains(down, "DNAT") {
		t.Fatalf("down should remove host-gateway chains without installing DNAT:\n%s", down)
	}

	old := newHarness(t, "hosts.yaml", nil)
	old.fake.track = true
	old.fake.version = "wslc version 3.0.1.0"
	old.fake.images["alpine"] = true
	old.fake.runtime = h.fake.runtime
	if err := old.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("old wslc: %v\n%s", err, old.stderr.String())
	}
	if strings.Contains(old.fake.mutations(), "system session") || !strings.Contains(old.stderr.String(), "published-port loopback") {
		t.Fatalf("old wslc must skip session rules and warn:\n%s\n%s", old.fake.mutations(), old.stderr.String())
	}
	if !strings.Contains(old.fake.mutations(), "exec -u 0 hosts-web-1") {
		t.Fatal("/etc/hosts update should still run")
	}
	dry := newHarness(t, "hosts.yaml", func(o *Options) { o.DryRun = true })
	dry.fake.track = true
	dry.fake.images["alpine"] = true
	dry.fake.runtime = h.fake.runtime
	if err := dry.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "dryrun_hosts.golden", scrub(dry.stdout.String()))
}

func TestUpSessionInvalidHandleTrustsStatus(t *testing.T) {
	runtime := map[string]map[string]any{
		"hosts-web-1": {
			"HostConfig": map[string]any{"NetworkMode": "hosts_default"},
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{
					"hosts_default": map[string]any{"IPAddress": "172.28.0.2", "Gateway": "172.28.0.1"},
				},
			},
			"Ports": map[string]any{
				"80/tcp": []any{map[string]any{"HostIp": "0.0.0.0", "HostPort": "8080"}},
			},
		},
	}
	h := newHarness(t, "hosts.yaml", nil)
	h.fake.track = true
	h.fake.images["alpine"] = true
	h.fake.runtime = runtime
	h.fake.sessionFlaky = true
	h.fake.sessionCode = 0
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("status 0 must succeed: %v\n%s", err, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "says exit 0") {
		t.Fatalf("expected status recovery in the log:\n%s", h.stderr.String())
	}
	if len(h.fake.containers) == 0 {
		t.Fatal("up removed the project after the script had exited 0")
	}
	marked, err := h.app.gatewayProgrammed("hosts")
	if err != nil || !marked {
		t.Fatalf("programmed rules must be recorded: %v %v", marked, err)
	}
	scriptCalls := 0
	for _, c := range h.fake.calls {
		if strings.Contains(c, "system session run") && !strings.Contains(c, "cat /run/") {
			scriptCalls++
		}
	}
	if scriptCalls != 1 {
		t.Fatalf("status 0 must not re-run the script, calls=%d\n%v", scriptCalls, h.fake.calls)
	}

	bad := newHarness(t, "hosts.yaml", nil)
	bad.fake.track = true
	bad.fake.images["alpine"] = true
	bad.fake.runtime = runtime
	bad.fake.sessionFlaky = true
	bad.fake.sessionCode = 72
	err = bad.app.Up(ctx, UpOptions{Detach: true, Timeout: -1})
	if err == nil || !strings.Contains(err.Error(), "72") {
		t.Fatalf("status 72: %v", err)
	}
	if len(bad.fake.containers) != 0 {
		t.Fatal("a real script failure must still roll the project back")
	}
}

func TestLegacy301Goldens(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	h.fake.version = "wslc version 3.0.1.0"
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("up minimal: %v\n%s", err, h.stderr.String())
	}
	golden.Assert(t, "legacy301_up_minimal.golden", h.fake.mutations())

	h = newHarness(t, "full.yaml", nil)
	h.fake.version = "wslc version 3.0.1.0"
	h.fake.health["full-api-1"] = []string{"starting", "healthy"}
	h.fake.health["full-api-2"] = []string{"healthy"}
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1, WaitTimeout: 5 * time.Second}); err != nil {
		t.Fatalf("up full: %v\n%s", err, h.stderr.String())
	}
	golden.Assert(t, "legacy301_up_full.golden", h.fake.mutations())

	h = newHarness(t, "full.yaml", nil)
	h.fake.version = "wslc version 3.0.1.0"
	h.fake.networks = []string{"full_front", "full_back", "unrelated"}
	h.fake.volumes = []string{"full_cache", "shared-dbdata"}
	h.fake.containers = append(h.fake.containers,
		container("full", "db", 1, "h", true),
		container("full", "web", 1, "h", true),
		container("full", "api", 2, "h", false),
		container("full", "api", 1, "h", true),
		container("full", "legacy", 1, "h", true),
	)
	if err := h.app.Down(ctx, DownOptions{Volumes: true, Timeout: 2}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "legacy301_down_full.golden", h.fake.mutations())
}

func TestDownNetworksSecretsHosts(t *testing.T) {
	h := newHarness(t, "networks.yaml", nil)
	h.fake.networks = []string{"nets_back", "nets_front", "already"}
	if err := h.app.Down(ctx, DownOptions{Timeout: 2}); err != nil {
		t.Fatal(err)
	}
	m := h.fake.mutations()
	if strings.Contains(m, "already") {
		t.Fatalf("external network removed:\n%s", m)
	}
	golden.Assert(t, "down_networks.golden", m)

	h = newHarness(t, "secrets.yaml", func(o *Options) {
		o.Env = []string{"API_KEY=s3cret-value"}
	})
	dir := filepath.Join(h.app.o.StateDir, "sec", "secrets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api_key"), []byte("s3cret-value"), 0o400); err != nil {
		t.Fatal(err)
	}
	h.fake.networks = []string{"sec_default"}
	h.fake.containers = append(h.fake.containers, container("sec", "app", 1, "h", true))
	if err := h.app.Down(ctx, DownOptions{Timeout: 2}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "down_secrets.golden", h.fake.mutations())
	if _, err := os.Stat(filepath.Join(h.app.o.StateDir, "sec")); !os.IsNotExist(err) {
		t.Fatalf("down left state dir: %v", err)
	}

	h = newHarness(t, "hosts.yaml", nil)
	h.fake.networks = []string{"hosts_default"}
	h.fake.containers = append(h.fake.containers, hostGatewayContainer("hosts", "web", 1, "h", true))
	if err := h.app.Down(ctx, DownOptions{Timeout: 2}); err != nil {
		t.Fatal(err)
	}
	down := h.fake.mutations()
	if !strings.Contains(down, "system session run") {
		t.Fatalf("down with a host-gateway container must run cleanup:\n%s", down)
	}
	golden.Assert(t, "down_hosts.golden", down)

	h = newHarness(t, "hosts.yaml", nil)
	h.fake.networks = []string{"hosts_default"}
	if err := h.app.Down(ctx, DownOptions{Timeout: 2}); err != nil {
		t.Fatalf("empty down: %v", err)
	}
	if strings.Contains(h.fake.mutations(), "system session") {
		t.Fatalf("empty down must not call session:\n%s", h.fake.mutations())
	}
	if !strings.Contains(h.fake.mutations(), "network remove hosts_default") {
		t.Fatalf("empty down must still remove networks:\n%s", h.fake.mutations())
	}

	h = newHarness(t, "hosts.yaml", nil)
	h.fake.networks = []string{"hosts_default"}
	h.fake.containers = append(h.fake.containers, container("hosts", "web", 1, "h", true))
	if err := h.app.Down(ctx, DownOptions{Timeout: 2}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.fake.mutations(), "system session") {
		t.Fatalf("unlabeled containers are not enough to clean rules:\n%s", h.fake.mutations())
	}

	h = newHarness(t, "hosts.yaml", nil)
	if err := h.app.noteGatewayProgrammed("hosts"); err != nil {
		t.Fatal(err)
	}
	h.fake.networks = []string{"hosts_default"}
	if err := h.app.Down(ctx, DownOptions{Timeout: 2}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.fake.mutations(), "system session run") {
		t.Fatalf("programmed rules must be removed even with no containers:\n%s", h.fake.mutations())
	}
	if marked, err := h.app.gatewayProgrammed("hosts"); err != nil || marked {
		t.Fatalf("successful cleanup must clear the marker: %v %v", marked, err)
	}

	h = newHarness(t, "hosts.yaml", nil)
	h.fake.failSession = true
	h.fake.networks = []string{"hosts_default"}
	h.fake.containers = append(h.fake.containers, hostGatewayContainer("hosts", "web", 1, "h", true))
	err := h.app.Down(ctx, DownOptions{Timeout: 2})
	if err == nil || !strings.Contains(err.Error(), "iptables") {
		t.Fatalf("gateway failure must surface: %v", err)
	}
	if !strings.Contains(h.fake.mutations(), "network remove hosts_default") {
		t.Fatalf("down must still remove networks:\n%s", h.fake.mutations())
	}
	if marked, merr := h.app.gatewayProgrammed("hosts"); merr != nil || !marked {
		t.Fatalf("failed cleanup must keep the programmed marker: %v %v", marked, merr)
	}
}

func TestRunMultiNetwork(t *testing.T) {
	h := newHarness(t, "networks.yaml", nil)
	h.fake.networks = []string{"already", "nets_back", "nets_front"}
	h.fake.images["alpine"] = true
	if err := h.app.Run(ctx, RunOptions{Service: "app", Detach: true, NoDeps: true, Name: "oneoff", NoTTY: true}); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	m := h.fake.mutations()
	if strings.Contains(m, "\nwslc run ") || strings.HasPrefix(m, "wslc run ") {
		t.Fatalf("detached multi-network run must create, connect, then start:\n%s", m)
	}
	golden.Assert(t, "run_multinet.golden", m)
}

func TestUpRollbackRemovesNewState(t *testing.T) {
	h := newHarness(t, "secrets.yaml", func(o *Options) {
		o.Env = []string{"API_KEY=s3cret-value"}
	})
	h.fake.failRun = "sec-app-1"
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err == nil {
		t.Fatal("up must fail")
	}
	if _, err := os.Stat(filepath.Join(h.app.o.StateDir, "sec")); !os.IsNotExist(err) {
		t.Fatalf("rollback left state dir: %v", err)
	}
}

func TestOldWslcIPAMIsWarning(t *testing.T) {
	cases := []struct {
		name, body, warn string
	}{
		{"replicas", `
name: replicas
services:
  web:
    image: alpine
    deploy:
      replicas: 2
    networks:
      n:
        ipv4_address: 10.0.0.10
networks:
  n:
    ipam:
      config:
        - subnet: 10.0.0.0/24
          gateway: 10.0.0.1
`, "static IPv4"},
		{"outside", `
name: outside
services:
  web:
    image: alpine
    networks:
      n:
        ipv4_address: 10.1.0.10
networks:
  n:
    ipam:
      config:
        - subnet: 10.0.0.0/24
          gateway: 10.0.0.1
`, "static IPv4"},
		{"gateway", `
name: gateway
services:
  web:
    image: alpine
    networks: [n]
networks:
  n:
    ipam:
      config:
        - gateway: 10.0.0.1
`, "requires a subnet"},
		{"slash32", `
name: slash32
services:
  web:
    image: alpine
    networks: [n]
networks:
  n:
    ipam:
      config:
        - subnet: 10.0.0.0/32
`, "broadcast"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(path, []byte(strings.TrimSpace(tc.body)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, "minimal.yaml", func(o *Options) { o.Files = []string{path} })
			h.fake.version = "wslc version 3.0.1.0"
			h.fake.images["alpine"] = true
			if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
				t.Fatalf("3.0.1 up: %v\n%s", err, h.stderr.String())
			}
			if !strings.Contains(h.stderr.String(), tc.warn) {
				t.Fatalf("missing warning %q in\n%s", tc.warn, h.stderr.String())
			}
			if tc.name == "gateway" || tc.name == "slash32" {
				strict := newHarness(t, "minimal.yaml", func(o *Options) {
					o.Files = []string{path}
					o.Strict = true
				})
				strict.fake.version = "wslc version 3.0.1.0"
				strict.fake.images["alpine"] = true
				if err := strict.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
					t.Fatalf("v0.1.5 accepted this under --strict: %v\n%s", err, strict.stderr.String())
				}
			}
		})
	}
}

func TestSubnetPassedThroughOnCurrentWslc(t *testing.T) {
	cases := []struct {
		name, body, subnet, warn string
		external                 bool
	}{
		{"ipv6", `
name: v6only
services:
  web:
    image: alpine
    networks: [n]
networks:
  n:
    ipam:
      config:
        - subnet: fd00:1::/64
`, "fd00:1::/64", "invalid IPv4 subnet", false},
		{"slash31", `
name: slash31
services:
  web:
    image: alpine
    networks: [n]
networks:
  n:
    ipam:
      config:
        - subnet: 10.0.0.0/31
`, "10.0.0.0/31", "broadcast", false},
		{"external", `
name: extipam
services:
  web:
    image: alpine
    networks:
      ext:
        ipv4_address: 10.9.9.9
networks:
  ext:
    external: true
    name: already
    ipam:
      config:
        - subnet: fd00:1::/64
`, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(path, []byte(strings.TrimSpace(tc.body)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, "minimal.yaml", func(o *Options) { o.Files = []string{path} })
			h.fake.images["alpine"] = true
			if tc.external {
				h.fake.networks = []string{"already"}
			}
			if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
				t.Fatalf("up: %v\n%s", err, h.stderr.String())
			}
			m := h.fake.mutations()
			if tc.external {
				if strings.Contains(m, "network create") {
					t.Fatalf("external network was created:\n%s", m)
				}
				if strings.Contains(h.stderr.String(), "invalid IPv4") || strings.Contains(h.stderr.String(), "subnet") {
					t.Fatalf("external network was validated:\n%s", h.stderr.String())
				}
				return
			}
			if !strings.Contains(m, "network create") || !strings.Contains(m, "--subnet "+tc.subnet) {
				t.Fatalf("network create missing --subnet %s:\n%s", tc.subnet, m)
			}
			if !strings.Contains(h.stderr.String(), tc.warn) {
				t.Fatalf("missing warning %q in\n%s", tc.warn, h.stderr.String())
			}
		})
	}
}

func TestIPRangeWithoutIPv4SubnetDoesNotMutate(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"no-subnet", `
name: nosub
services:
  web:
    image: alpine:missing
    networks: [n]
networks:
  n:
    ipam:
      config:
        - ip_range: 10.1.0.0/24
`},
		{"ipv6", `
name: v6range
services:
  web:
    image: alpine:missing
    networks: [n]
networks:
  n:
    ipam:
      config:
        - subnet: fd00:1::/64
          ip_range: fd00:1::/80
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(path, []byte(strings.TrimSpace(tc.body)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, "minimal.yaml", func(o *Options) { o.Files = []string{path} })
			err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1})
			if err == nil || !strings.Contains(err.Error(), "ip_range") {
				t.Fatalf("up: %v", err)
			}
			assertVersionOnly(t, h.fake)
		})
	}
}

func TestOldWslcOmitsIPRange(t *testing.T) {
	cases := []struct {
		name, body, subnet string
	}{
		{"no-subnet", `
name: nosub
services:
  web:
    image: alpine
    networks: [n]
networks:
  n:
    ipam:
      config:
        - ip_range: 10.1.0.0/24
`, ""},
		{"ipv6", `
name: v6range
services:
  web:
    image: alpine
    networks: [n]
networks:
  n:
    ipam:
      config:
        - subnet: fd00:1::/64
          ip_range: fd00:1::/80
`, "fd00:1::/64"},
		{"outside", `
name: range
services:
  web:
    image: alpine
    networks: [n]
networks:
  n:
    ipam:
      config:
        - subnet: 10.0.0.0/24
          ip_range: 10.1.0.0/24
`, "10.0.0.0/24"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(path, []byte(strings.TrimSpace(tc.body)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, "minimal.yaml", func(o *Options) { o.Files = []string{path} })
			h.fake.version = "wslc version 3.0.1.0"
			h.fake.images["alpine"] = true
			if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
				t.Fatalf("3.0.1 up: %v\n%s", err, h.stderr.String())
			}
			m := h.fake.mutations()
			if strings.Contains(m, "--ip-range") {
				t.Fatalf("3.0.1 emitted --ip-range:\n%s", m)
			}
			if !strings.Contains(m, "network create") {
				t.Fatalf("3.0.1 did not create the network:\n%s", m)
			}
			if tc.subnet != "" && !strings.Contains(m, "--subnet "+tc.subnet) {
				t.Fatalf("3.0.1 missing --subnet %s:\n%s", tc.subnet, m)
			}
			if !strings.Contains(h.stderr.String(), "ip_range") {
				t.Fatalf("missing ip_range warning:\n%s", h.stderr.String())
			}
		})
	}
}

func TestIPRangeOutsideSubnetDoesNotMutate(t *testing.T) {
	body := `
name: range
services:
  web:
    image: alpine:missing
    networks: [n]
networks:
  n:
    ipam:
      config:
        - subnet: 10.0.0.0/24
          ip_range: 10.1.0.0/24
`
	path := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, "minimal.yaml", func(o *Options) { o.Files = []string{path} })
	err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1})
	if err == nil || !strings.Contains(err.Error(), "ip_range") {
		t.Fatalf("up: %v", err)
	}
	if m := h.fake.mutations(); m != "" {
		t.Fatalf("ip_range failure mutated wslc:\n%s", m)
	}
}

func TestRunValidatesBeforeCreate(t *testing.T) {
	body := `
name: badrun
services:
  web:
    image: alpine:missing
    networks:
      n:
        ipv4_address: 10.0.0.10
networks:
  n:
    ipam:
      config:
        - subnet: 10.0.0.0/24
          ip_range: 10.1.0.0/24
`
	path := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, "minimal.yaml", func(o *Options) { o.Files = []string{path} })
	err := h.app.Run(ctx, RunOptions{Service: "web", NoDeps: true, NoTTY: true})
	if err == nil || !strings.Contains(err.Error(), "ip_range") {
		t.Fatalf("run: %v", err)
	}
	if m := h.fake.mutations(); m != "" {
		t.Fatalf("validation failure mutated wslc:\n%s", m)
	}
}

func TestStrictUnknownAllowsInspectAndDown(t *testing.T) {
	strict := func() *harness {
		t.Helper()
		h := newHarness(t, "minimal.yaml", func(o *Options) { o.Strict = true })
		h.fake.version = "not a version"
		return h
	}
	up := strict()
	if err := up.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); !errors.Is(err, project.ErrStrict) {
		t.Fatalf("up: %v", err)
	}
	assertVersionOnly(t, up.fake)

	down := strict()
	if err := down.app.Down(ctx, DownOptions{Timeout: -1}); err != nil {
		t.Fatalf("down: %v\n%s", err, down.stderr.String())
	}
	if m := down.fake.mutations(); m != "" {
		t.Fatalf("down mutated:\n%s", m)
	}
	ps := strict()
	if err := ps.app.Ps(ctx, PsOptions{}); err != nil {
		t.Fatalf("ps: %v\n%s", err, ps.stderr.String())
	}
	if m := ps.fake.mutations(); m != "" {
		t.Fatalf("ps mutated:\n%s", m)
	}
	stop := strict()
	if err := stop.app.Stop(ctx, nil, -1); err != nil {
		t.Fatalf("stop: %v\n%s", err, stop.stderr.String())
	}
	if m := stop.fake.mutations(); m != "" {
		t.Fatalf("stop mutated:\n%s", m)
	}
}

func TestStrictUnknownStopsRunAndBuild(t *testing.T) {
	run := newHarness(t, "minimal.yaml", func(o *Options) { o.Strict = true })
	run.fake.version = "not a version"
	err := run.app.Run(ctx, RunOptions{Service: "hello", NoDeps: true, NoTTY: true})
	if !errors.Is(err, project.ErrStrict) || !strings.Contains(err.Error(), "version unknown") {
		t.Fatalf("run --no-deps: %v", err)
	}
	assertVersionOnly(t, run.fake)

	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	body := "name: bld\nservices:\n  web:\n    image: example/web:dev\n    build:\n      context: .\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	bld := newHarness(t, "minimal.yaml", func(o *Options) {
		o.Files = []string{path}
		o.Strict = true
	})
	bld.fake.version = "not a version"
	err = bld.app.Build(ctx, BuildOptions{})
	if !errors.Is(err, project.ErrStrict) || !strings.Contains(err.Error(), "version unknown") {
		t.Fatalf("build: %v\n%s", err, bld.stderr.String())
	}
	assertVersionOnly(t, bld.fake)
}

func assertVersionOnly(t *testing.T, f *fakeWSLC) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("expected a version probe")
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "wslc --version") && !strings.HasPrefix(c, "wslc version") {
			t.Fatalf("wslc call before the strict failure: %s", c)
		}
	}
}

func TestUnknownVersionSkipsImageChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	write := func(image string) {
		t.Helper()
		body := "name: minimal\nservices:\n  hello:\n    image: " + image + "\n    ports: [\"8080:80\"]\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nginx:alpine")
	h := newHarness(t, "minimal.yaml", func(o *Options) { o.Files = []string{path} })
	h.fake.images["nginx:alpine"] = true
	h.fake.images["nginx:latest"] = true
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("up: %v\n%s", err, h.stderr.String())
	}
	write("nginx:latest")
	h.fake.version = "not a version"
	h.fake.calls = nil
	h.reopen()
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("non-strict: %v\n%s", err, h.stderr.String())
	}
	if m := h.fake.mutations(); strings.Contains(m, "run ") || strings.Contains(m, "remove ") {
		t.Fatalf("unknown version recreated hello:\n%s", m)
	}
	if !strings.Contains(h.stderr.String(), "service hello") || !strings.Contains(h.stderr.String(), "not applied") {
		t.Fatalf("missing per-service warning:\n%s", h.stderr.String())
	}
	h.app.o.Strict = true
	h.fake.calls = nil
	h.reopen()
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); !errors.Is(err, project.ErrStrict) {
		t.Fatalf("strict unknown version: %v", err)
	}
}

func TestUpgradeRecreatesOnlyChangedCommands(t *testing.T) {
	h := newHarness(t, "upgrade.yaml", nil)
	h.fake.version = "wslc version 3.0.1.0"
	h.fake.images["alpine"] = true
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("3.0.1: %v\n%s", err, h.stderr.String())
	}
	h.fake.version = "wslc version 3.0.3.0"
	h.fake.calls = nil
	h.reopen()
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("3.0.3: %v\n%s", err, h.stderr.String())
	}
	m := h.fake.mutations()
	for _, name := range []string{"upg-multi-1", "upg-addressed-1"} {
		if !strings.Contains(m, name) {
			t.Errorf("upgrade should recreate %s:\n%s", name, m)
		}
	}
	for _, name := range []string{"upg-plain-1", "upg-extsec-1", "upg-extcfg-1", "upg-ranged-1", "upg-built-1"} {
		if strings.Contains(m, name) {
			t.Errorf("upgrade recreated %s whose run command did not change:\n%s", name, m)
		}
	}
}

func TestUnchangedHashSurvivesUpgrade(t *testing.T) {
	const helloHash = "0df62ea0f7a4228c79ebe55b0d198f8ce5de02a73ed48a3d6d6d15a7257493bc"
	for _, version := range []string{"wslc version 3.0.1.0", "wslc version 3.0.3.0"} {
		h := newHarness(t, "minimal.yaml", nil)
		h.fake.version = version
		h.fake.networks = []string{"minimal_default"}
		h.fake.images["nginx:alpine"] = true
		h.fake.containers = append(h.fake.containers, container("minimal", "hello", 1, helloHash, true))
		if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
			t.Fatalf("%s: %v\n%s", version, err, h.stderr.String())
		}
		if m := h.fake.mutations(); m != "" {
			t.Fatalf("%s recreated an unchanged container:\n%s", version, m)
		}
	}
}

func TestUnknownVersionDoesNotRecreate(t *testing.T) {
	h := newHarness(t, "networks.yaml", nil)
	h.fake.networks = []string{"already"}
	h.fake.images["alpine"] = true
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("create: %v\n%s", err, h.stderr.String())
	}
	h.fake.calls = nil
	h.reopen()
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("3.0.3 baseline: %v\n%s", err, h.stderr.String())
	}
	if m := h.fake.mutations(); m != "" {
		t.Fatalf("3.0.3 baseline mutated:\n%s", m)
	}
	h.fake.version = "not a version"
	h.fake.calls = nil
	h.reopen()
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("unknown version: %v\n%s", err, h.stderr.String())
	}
	if m := h.fake.mutations(); m != "" {
		t.Fatalf("unknown version recreated containers:\n%s", m)
	}
	if !strings.Contains(h.stderr.String(), "will not be recreated") {
		t.Fatalf("missing warning:\n%s", h.stderr.String())
	}
	if strings.Contains(h.stderr.String(), "not applied") {
		t.Fatalf("unchanged 3.0.3 config must not warn:\n%s", h.stderr.String())
	}
	h.fake.version = "wslc version 3.0.3.0"
	h.fake.calls = nil
	h.reopen()
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatalf("recovered: %v\n%s", err, h.stderr.String())
	}
	if m := h.fake.mutations(); m != "" {
		t.Fatalf("recovered version mutated:\n%s", m)
	}

	strict := newHarness(t, "networks.yaml", func(o *Options) { o.Strict = true })
	strict.fake.version = "not a version"
	strict.fake.networks = []string{"already"}
	strict.fake.images["alpine"] = true
	if err := strict.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); !errors.Is(err, project.ErrStrict) {
		t.Fatalf("strict + unknown version: %v", err)
	}
}

func TestRunAndBuildFailureRemoveState(t *testing.T) {
	h := newHarness(t, "secrets.yaml", func(o *Options) {
		o.Env = []string{"API_KEY=s3cret-value"}
	})
	h.fake.images["alpine"] = true
	h.fake.images["busybox"] = true
	h.fake.networks = []string{"sec_default"}
	h.fake.failRun = "oneoff"
	if err := h.app.Run(ctx, RunOptions{Service: "app", NoDeps: true, NoTTY: true, Name: "oneoff"}); err == nil {
		t.Fatal("run must fail")
	}
	if _, err := os.Stat(filepath.Join(h.app.o.StateDir, "sec")); !os.IsNotExist(err) {
		t.Fatalf("failed run left state dir: %v", err)
	}

	h = newHarness(t, "secrets.yaml", func(o *Options) {
		o.Env = []string{"API_KEY=s3cret-value"}
	})
	h.fake.failBuild = true
	if err := h.app.Build(ctx, BuildOptions{}); err == nil {
		t.Fatal("build must fail")
	}
	if _, err := os.Stat(filepath.Join(h.app.o.StateDir, "sec")); !os.IsNotExist(err) {
		t.Fatalf("failed build left state dir: %v", err)
	}
}

func TestDefaultStateRoot(t *testing.T) {
	root, err := defaultStateRoot()
	if err != nil || root == "" {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local != "" && !strings.HasPrefix(strings.ToLower(root), strings.ToLower(local)) {
			t.Fatalf("state root %s is not under LocalAppData %s", root, local)
		}
	}
}

func TestUpIsIdempotent(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	h.fake.networks = []string{"minimal_default"}
	h.fake.images["nginx:alpine"] = true
	h.fake.containers = append(h.fake.containers, container("minimal", "hello", 1, hashOf(t, h, "hello"), true))
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	if m := h.fake.mutations(); m != "" {
		t.Fatalf("expected no mutations, got:\n%s", m)
	}
	if !strings.Contains(h.stderr.String(), "minimal-hello-1  Running") {
		t.Fatalf("missing status line: %s", h.stderr.String())
	}
}

func TestUpRecreatesOnHashChange(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	h.fake.networks = []string{"minimal_default"}
	h.fake.images["nginx:alpine"] = true
	h.fake.containers = append(h.fake.containers,
		container("minimal", "hello", 1, "stale-hash", true),
		container("minimal", "gone", 1, "x", false), // orphan
	)
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: 3, RemoveOrphans: true}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "up_recreate.golden", h.fake.mutations())
}

func TestUpForceRecreateAndStartStopped(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	h.fake.networks = []string{"minimal_default"}
	h.fake.images["nginx:alpine"] = true
	h.fake.containers = append(h.fake.containers, container("minimal", "hello", 1, hashOf(t, h, "hello"), false))
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.mutations(); got != "wslc start minimal-hello-1\n" {
		t.Fatalf("stopped container should be started, got %q", got)
	}
	h.fake.calls = nil
	if err := h.app.Up(ctx, UpOptions{Detach: true, ForceRecreate: true, Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.mutations(); !strings.HasPrefix(got, "wslc remove -f minimal-hello-1\nwslc run -d") {
		t.Fatalf("force recreate: got %q", got)
	}
}

func TestUpBuildForcesRecreate(t *testing.T) {
	h := newHarness(t, "profiles.yaml", nil)
	h.fake.networks = []string{"profiles_default"}
	h.fake.images["busybox"] = true
	h.fake.images["profiles-app"] = true
	h.fake.containers = append(h.fake.containers,
		container("profiles", "app", 1, hashOf(t, h, "app"), true),
		container("profiles", "migrate", 1, hashOf(t, h, "migrate"), false))
	h.fake.exited["profiles-migrate-1"] = 0
	if err := h.app.Up(ctx, UpOptions{Detach: true, Build: true, Timeout: -1, WaitTimeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	m := h.fake.mutations()
	if !strings.Contains(m, "wslc build -t profiles-app") || !strings.Contains(m, "wslc stop profiles-app-1") ||
		strings.Contains(m, "remove -f profiles-migrate-1") {
		t.Fatalf("unexpected mutations:\n%s", m)
	}
}

func TestUpFailsOnUnhealthyDependency(t *testing.T) {
	h := newHarness(t, "full.yaml", nil)
	h.fake.health["full-api-1"] = []string{"unhealthy"}
	err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1, WaitTimeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("expected unhealthy error, got %v", err)
	}
	if strings.Contains(h.fake.mutations(), "--name full-web") {
		t.Fatal("dependent service must not start")
	}
}

func TestStrictBlocksBeforeAnyCall(t *testing.T) {
	h := newHarness(t, "unsupported.yaml", func(o *Options) { o.Strict = true })
	if err := h.app.Up(ctx, UpOptions{Detach: true}); !errors.Is(err, project.ErrStrict) {
		t.Fatalf("expected ErrStrict, got %v", err)
	}
	if len(h.fake.calls) != 1 || !strings.Contains(h.fake.calls[0], "--version") {
		t.Fatalf("strict mode must fail before mutating, got %v", h.fake.calls)
	}
}

func TestDryRunWithoutWslc(t *testing.T) {
	h := newHarness(t, "minimal.yaml", func(o *Options) { o.DryRun = true })
	missing := runnerFunc(func() error { return errors.New("exec: wslc: not found") })
	app := New(h.app.o, missing, golden.Paths, h.app.io)
	if err := app.Up(ctx, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "dryrun_minimal.golden", hashRE.ReplaceAllString(h.stdout.String(), "config-hash=<hash>"))
}

func TestDown(t *testing.T) {
	h := newHarness(t, "full.yaml", nil)
	h.fake.networks = []string{"full_front", "full_back", "unrelated"}
	h.fake.volumes = []string{"full_cache", "shared-dbdata"}
	h.fake.containers = append(h.fake.containers,
		container("full", "db", 1, "h", true),
		container("full", "web", 1, "h", true),
		container("full", "api", 2, "h", false),
		container("full", "api", 1, "h", true),
		container("full", "legacy", 1, "h", true),
	)
	if err := h.app.Down(ctx, DownOptions{Volumes: true, Timeout: 2}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "down_full.golden", h.fake.mutations())
	if !strings.Contains(h.stderr.String(), "orphan") {
		t.Fatal("orphan warning missing")
	}
}

func TestStartStopRestart(t *testing.T) {
	h := newHarness(t, "profiles.yaml", nil)
	h.fake.containers = append(h.fake.containers,
		container("profiles", "app", 1, "h", false),
		container("profiles", "migrate", 1, "h", false),
		container("profiles", "debug", 1, "h", true))
	if err := h.app.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Stop(ctx, []string{"debug"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Restart(ctx, []string{"app"}, -1); err != nil {
		t.Fatal(err)
	}
	want := "wslc start profiles-migrate-1\nwslc start profiles-app-1\nwslc stop -t 1 profiles-debug-1\nwslc start profiles-app-1\n"
	if got := h.fake.mutations(); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if err := h.app.Start(ctx, []string{"nope"}); err == nil {
		t.Fatal("unknown service must fail")
	}
}

func TestPsAndLogs(t *testing.T) {
	h := newHarness(t, "full.yaml", nil)
	h.fake.containers = append(h.fake.containers,
		container("full", "web", 1, "h", true),
		container("full", "db", 1, "h", false))
	h.fake.logs["full-web-1"] = "GET /\nGET /health\n"
	h.fake.logs["full-db-1"] = "ready"
	if err := h.app.Ps(ctx, PsOptions{All: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Logs(ctx, LogsOptions{Services: []string{"web", "db"}}); err != nil {
		t.Fatal(err)
	}
	out := h.stdout.String()
	for _, want := range []string{"NAME", "full-db-1", "exited", "full-db-1  | ready", "full-web-1 | GET /health"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "full-db-1   img") > strings.Index(out, "full-web-1   img") {
		t.Errorf("ps must list dependencies first:\n%s", out)
	}
	h.stdout.Reset()
	if err := h.app.Ps(ctx, PsOptions{Quiet: true}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(h.stdout.String()) != "db-1\nweb-1" {
		t.Fatalf("quiet ps: %q", h.stdout.String())
	}
}

func TestExecAndRun(t *testing.T) {
	h := newHarness(t, "full.yaml", nil)
	if err := h.app.Exec(ctx, ExecOptions{Service: "api", Index: 2, Command: []string{"sh", "-c", "echo hi"}, Interactive: true, TTY: true, User: "root", Env: []string{"A=1"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Exec(ctx, ExecOptions{Service: "api"}); err == nil {
		t.Fatal("exec without command must fail")
	}
	h.fake.images["postgres:16-alpine"] = true
	h.fake.networks = []string{"full_back"}
	h.fake.volumes = []string{"shared-dbdata"}
	if err := h.app.Run(ctx, RunOptions{Service: "db", Name: "oneoff", Remove: true, NoDeps: true, NoTTY: true, Command: []string{"psql", "--version"}}); err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "exec_run.golden", h.fake.mutations())
}

func TestBuildPullConfigVersion(t *testing.T) {
	h := newHarness(t, "full.yaml", nil)
	if err := h.app.Build(ctx, BuildOptions{NoCache: true, Pull: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Pull(ctx, PullOptions{}); err != nil {
		t.Fatal(err)
	}
	want := "wslc build -t example/web:dev -f C:/proj/web/Dockerfile.dev --build-arg VERSION=1.2 --target runtime -l org.example.stage=dev --pull --no-cache C:/proj/web\n" +
		"wslc pull example/api:1.0\nwslc pull postgres:16-alpine\nwslc pull example/web:dev\n"
	if got := h.fake.mutations(); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	for _, o := range []ConfigOptions{{Services: true}, {Volumes: true}, {Networks: true}, {Images: true}, {Format: "json"}, {}} {
		if err := h.app.Config(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	out := h.stdout.String()
	for _, want := range []string{"api\ndb\nweb\n", "shared-dbdata", "full_front", "example/web:dev", `"name": "full"`, "name: full"} {
		if !strings.Contains(out, want) {
			t.Errorf("config output missing %q", want)
		}
	}
	h.stdout.Reset()
	h.app.PrintVersion(ctx, false)
	if !strings.Contains(h.stdout.String(), "wslc: wslc version 3.0.3.0") {
		t.Fatalf("version: %s", h.stdout.String())
	}
}

func TestUpAttachedStopsOnInterrupt(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	h.fake.images["nginx:alpine"] = true
	h.fake.networks = []string{"minimal_default"}
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		for !strings.Contains(h.fake.mutations(), "logs -f") {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := h.app.Up(cctx, UpOptions{Timeout: 4}); err != nil {
		t.Fatal(err)
	}
	m := h.fake.mutations()
	if !strings.Contains(m, "wslc logs -f minimal-hello-1") || !strings.HasSuffix(m, "wslc stop -t 4 minimal-hello-1\n") {
		t.Fatalf("attach/stop sequence wrong:\n%s", m)
	}
}

func TestUpWait(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	h.fake.images["nginx:alpine"] = true
	if err := h.app.Up(ctx, UpOptions{Detach: true, Wait: true, WaitTimeout: time.Second, Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stderr.String(), "minimal-hello-1  ready") {
		t.Fatalf("missing ready line:\n%s", h.stderr.String())
	}
}

func TestSatisfied(t *testing.T) {
	tests := []struct {
		c       wslc.Container
		cond    waitCond
		done    bool
		wantErr bool
	}{
		{wslc.Container{State: "running", Health: "healthy"}, waitHealthy, true, false},
		{wslc.Container{State: "running", Health: "starting"}, waitHealthy, false, false},
		{wslc.Container{State: "running", Health: "unhealthy"}, waitHealthy, false, true},
		{wslc.Container{State: "running"}, waitHealthy, false, true}, // errNoHealth
		{wslc.Container{State: "running"}, waitReady, true, false},
		{wslc.Container{State: "exited", ExitCode: 0}, waitCompleted, true, false},
		{wslc.Container{State: "exited", ExitCode: 2}, waitCompleted, false, true},
		{wslc.Container{State: "created"}, waitCompleted, false, false},
		{wslc.Container{State: "running"}, waitStarted, true, false},
	}
	for i, tt := range tests {
		done, err := satisfied(tt.c, tt.cond)
		if done != tt.done || (err != nil) != tt.wantErr {
			t.Errorf("case %d: got %v,%v", i, done, err)
		}
	}
}

func TestWaitDegradesWithoutHealth(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	if err := h.app.waitFor(ctx, "x", waitHealthy, time.Second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stderr.String(), "no health status") {
		t.Fatal("expected degradation warning")
	}
	h.fake.exited["y"] = 0
	if err := h.app.waitFor(ctx, "y", waitHealthy, 20*time.Millisecond); err == nil {
		t.Fatal("expected timeout for exited container waiting on health")
	}
}

func TestUpRollsBackOnFailure(t *testing.T) {
	h := newHarness(t, "full.yaml", nil)
	h.fake.images = map[string]bool{"example/api:1.0": true, "postgres:16-alpine": true, "example/web:dev": true}
	h.fake.volumes = []string{"shared-dbdata"} // pre-existing: must survive
	h.fake.health["full-api-1"] = []string{"healthy"}
	h.fake.health["full-api-2"] = []string{"healthy"}
	h.fake.failRun = "full-web"
	err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1, WaitTimeout: time.Second})
	if err == nil {
		t.Fatal("up must fail")
	}
	m := h.fake.mutations()
	want := "wslc remove -f full-web\nwslc remove -f full-api-2\nwslc remove -f full-api-1\nwslc remove -f full-db-1\n" +
		"wslc volume remove full_web_scratch_anon\nwslc volume remove full_cache\n" +
		"wslc network remove full_front\nwslc network remove full_back\n"
	if !strings.HasSuffix(m, want) {
		t.Fatalf("rollback mismatch:\n%s", m)
	}
	if strings.Contains(m, "remove shared-dbdata") || !strings.Contains(h.stderr.String(), "Rolling back") {
		t.Fatalf("pre-existing volume touched or no notice:\n%s\n%s", m, h.stderr.String())
	}
}

func TestUpScaleOverride(t *testing.T) {
	h := newHarness(t, "minimal.yaml", nil)
	h.fake.networks = []string{"minimal_default"}
	h.fake.images["nginx:alpine"] = true
	hash := hashOf(t, h, "hello")
	h.fake.containers = append(h.fake.containers,
		container("minimal", "hello", 1, hash, true), container("minimal", "hello", 2, hash, true))
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1, Scale: map[string]int{"hello": 1}}); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.mutations(); got != "wslc remove -f minimal-hello-2\n" && !strings.Contains(got, "stop minimal-hello-2") {
		t.Fatalf("scale down: %q", got)
	}
	h.fake.calls = nil
	h.fake.containers = h.fake.containers[:1]
	if err := h.app.Up(ctx, UpOptions{Detach: true, Timeout: -1, Scale: map[string]int{"hello": 3}}); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.mutations(); strings.Count(got, "wslc run -d") != 2 || !strings.Contains(got, "minimal-hello-3") {
		t.Fatalf("scale up: %q", got)
	}
	if err := h.app.Up(ctx, UpOptions{Detach: true, Scale: map[string]int{"nope": 1}}); err == nil {
		t.Fatal("unknown service accepted")
	}
}

func TestPsServicesStatusHealth(t *testing.T) {
	h := newHarness(t, "full.yaml", nil)
	h.fake.containers = append(h.fake.containers,
		container("full", "web", 1, "h", true),
		container("full", "api", 1, "h", true),
		container("full", "db", 1, "h", false))
	h.fake.health["api-1"] = []string{"healthy"} // inspected by ID
	if err := h.app.Ps(ctx, PsOptions{ListServices: true, All: true}); err != nil {
		t.Fatal(err)
	}
	if got := h.stdout.String(); got != "db\napi\nweb\n" {
		t.Fatalf("--services: %q", got)
	}
	h.stdout.Reset()
	if err := h.app.Ps(ctx, PsOptions{Status: []string{"exited"}}); err != nil {
		t.Fatal(err)
	}
	if out := h.stdout.String(); !strings.Contains(out, "full-db-1") || strings.Contains(out, "full-web-1") || strings.Contains(out, "HEALTH") {
		t.Fatalf("--status exited:\n%s", out)
	}
	h.stdout.Reset()
	if err := h.app.Ps(ctx, PsOptions{Status: []string{"running"}}); err != nil {
		t.Fatal(err)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "HEALTH") || !strings.Contains(out, "healthy") || strings.Contains(out, "full-db-1") {
		t.Fatalf("--status running with health:\n%s", out)
	}
	// web declares a healthcheck too, but only api reports one; db is not inspected.
	if slices.Contains(h.fake.calls, "wslc inspect db-1") {
		t.Fatal("stopped containers must not be inspected")
	}
}
