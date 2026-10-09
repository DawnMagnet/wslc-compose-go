package app

import (
	"context"
	"errors"
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
	if len(h.fake.calls) != 0 {
		t.Fatalf("strict mode must fail before calling wslc: %v", h.fake.calls)
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
	if !strings.Contains(h.stdout.String(), "wslc: wslc 2.9.4.0") {
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
