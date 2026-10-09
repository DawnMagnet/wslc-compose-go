package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dawnmagnet/wslc-compose-go/internal/app"
	"github.com/dawnmagnet/wslc-compose-go/internal/golden"
	"github.com/dawnmagnet/wslc-compose-go/internal/wslc"
)

// run executes the CLI with a recording runner and returns stdout + calls.
func run(t *testing.T, args ...string) (string, []string, error) {
	t.Helper()
	var out bytes.Buffer
	var calls []string
	factory := func(o app.Options) *app.App {
		o.IgnoreOSEnv = true
		r := wslc.RunnerFunc(func(_ context.Context, a []string, _ wslc.IO) error {
			calls = append(calls, strings.Join(a, " "))
			return nil
		})
		return app.New(o, r, golden.Paths, wslc.IO{Stdout: &out, Stderr: &out})
	}
	root := NewRoot(factory)
	root.SetArgs(Normalize(append([]string{"-f", golden.Compose("minimal.yaml")}, args...)))
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.ExecuteContext(context.Background())
	return out.String(), calls, err
}

func TestUpDryRunFlags(t *testing.T) {
	out, calls, err := run(t, "--dry-run", "--default-dns", "9.9.9.9", "up", "-d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--dns 9.9.9.9") || !strings.Contains(out, "wslc run -d --name minimal-hello-1") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "run ") || strings.HasPrefix(c, "network create") {
			t.Fatalf("dry-run executed a mutation: %s", c)
		}
	}
}

func TestDefaultDNSCanBeDisabled(t *testing.T) {
	out, _, err := run(t, "--dry-run", "--default-dns=", "up", "-d")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--dns") {
		t.Fatalf("--default-dns= should disable DNS injection:\n%s", out)
	}
}

func TestExecPassesCommandVerbatim(t *testing.T) {
	_, calls, err := run(t, "exec", "-T", "-u", "app", "hello", "ls", "-la", "--color")
	if err != nil {
		t.Fatal(err)
	}
	if calls[len(calls)-1] != "exec -i -u app minimal-hello-1 ls -la --color" {
		t.Fatalf("calls: %v", calls)
	}
}

func TestDashDashIsStripped(t *testing.T) {
	_, calls, err := run(t, "exec", "-T", "hello", "--", "ls", "-la")
	if err != nil || calls[len(calls)-1] != "exec -i minimal-hello-1 ls -la" {
		t.Fatalf("exec: %v %v", err, calls)
	}
	out, _, err := run(t, "--dry-run", "run", "--rm", "-T", "--no-deps", "hello", "--", "echo", "hi")
	if err != nil || !strings.Contains(out, "nginx:alpine echo hi") {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if _, _, err := run(t, "exec", "hello", "--"); err == nil {
		t.Fatal("exec with only -- accepted")
	}
}

func TestUpScale(t *testing.T) {
	out, _, err := run(t, "--dry-run", "up", "-d", "--scale", "hello=3")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--name minimal-hello-1", "--name minimal-hello-3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"hello", "hello=x", "hello=-1", "=2", "nope=2"} {
		if _, _, err := run(t, "--dry-run", "up", "-d", "--scale", bad); err == nil {
			t.Errorf("--scale %s accepted", bad)
		}
	}
}

func TestConfigListingsAreQuiet(t *testing.T) {
	var out bytes.Buffer
	factory := func(o app.Options) *app.App {
		o.IgnoreOSEnv = true
		return app.New(o, wslc.RunnerFunc(func(context.Context, []string, wslc.IO) error { return nil }),
			golden.Paths, wslc.IO{Stdout: &out, Stderr: &out})
	}
	root := NewRoot(factory)
	root.SetArgs([]string{"-f", golden.Compose("full.yaml"), "config", "--services"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "api\ndb\nweb\n" {
		t.Fatalf("config --services must print only names, got:\n%s", out.String())
	}
}

func TestCommandsWire(t *testing.T) {
	for _, args := range [][]string{
		{"config", "--services"}, {"ps", "-a"}, {"ps", "--services", "--status", "running", "--status", "exited"}, {"logs", "-f", "--tail", "5"}, {"down", "-v"},
		{"start"}, {"stop", "-t", "1"}, {"restart"}, {"build", "--no-cache"}, {"pull"},
		{"version", "--short"}, {"run", "--rm", "-T", "--no-deps", "hello", "true"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, _, err := run(t, args...); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
		})
	}
	if _, _, err := run(t, "up", "--build", "--no-build"); err == nil {
		t.Fatal("mutually exclusive flags accepted")
	}
	if _, _, err := run(t, "exec", "hello"); err == nil {
		t.Fatal("exec without command accepted")
	}
}

func TestNormalize(t *testing.T) {
	got := strings.Join(Normalize([]string{"-f", "a.yaml", "--dry-run", "logs", "-f", "web", "--", "-f"}), " ")
	if got != "-f a.yaml --dry-run logs --follow web -- -f" {
		t.Fatalf("got %s", got)
	}
	if got := strings.Join(Normalize([]string{"-f", "logs", "up", "-f"}), " "); got != "-f logs up -f" {
		t.Fatalf("file named logs must not trigger rewrite: %s", got)
	}
}
