package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dawnmagnet/wslc-compose-go/internal/golden"
	"github.com/dawnmagnet/wslc-compose-go/internal/labels"
	"github.com/dawnmagnet/wslc-compose-go/internal/wslc"
)

// fakeWSLC emulates just enough of the wslc CLI for orchestration tests.
type fakeWSLC struct {
	mu         sync.Mutex
	calls      []string
	containers []map[string]any
	networks   []string
	volumes    []string
	images     map[string]bool
	health     map[string][]string // per-container sequence of health states
	logs       map[string]string
	exited     map[string]int // containers that report exited with this code
}

func (f *fakeWSLC) Run(ctx context.Context, args []string, stdio wslc.IO) error {
	if args[0] == "logs" && args[1] == "-f" { // follow blocks until interrupted
		f.mu.Lock()
		f.calls = append(f.calls, wslc.Format("wslc", args))
		f.mu.Unlock()
		if stdio.Stdout != nil {
			fmt.Fprintln(stdio.Stdout, "started")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, wslc.Format("wslc", args))
	out := stdio.Stdout
	if out == nil {
		out = io.Discard
	}
	switch strings.Join(args[:min(2, len(args))], " ") {
	case "network list":
		fmt.Fprintln(out, "NETWORK ID  NAME  DRIVER")
		for _, n := range f.networks {
			fmt.Fprintf(out, "x           %s  bridge\n", n)
		}
		return nil
	case "volume list":
		fmt.Fprintln(out, "DRIVER  VOLUME NAME")
		for _, v := range f.volumes {
			fmt.Fprintf(out, "local   %s\n", v)
		}
		return nil
	case "image inspect":
		if f.images[args[2]] {
			fmt.Fprintln(out, `[{"Id":"sha256:1"}]`)
			return nil
		}
		return errors.New("no such image")
	}
	switch args[0] {
	case "list":
		return json.NewEncoder(out).Encode(f.containers)
	case "inspect":
		name := args[1]
		states := f.health[name]
		state := map[string]any{"Status": "running"}
		if code, ok := f.exited[name]; ok {
			state = map[string]any{"Status": "exited", "ExitCode": code}
		}
		if len(states) > 0 {
			state["Health"] = map[string]any{"Status": states[0]}
			if len(states) > 1 {
				f.health[name] = states[1:]
			}
		}
		return json.NewEncoder(out).Encode([]any{map[string]any{"Name": name, "State": state}})
	case "logs":
		fmt.Fprint(out, f.logs[args[len(args)-1]])
	case "--version":
		fmt.Fprintln(out, "wslc 2.9.4.0")
	}
	return nil
}

var hashRE = regexp.MustCompile(`config-hash=[0-9a-f]{64}`)

// mutations returns recorded calls that change state (queries filtered out).
func (f *fakeWSLC) mutations() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for _, c := range f.calls {
		if strings.HasPrefix(c, "wslc list") || strings.HasPrefix(c, "wslc inspect") ||
			strings.Contains(c, " list") || strings.HasPrefix(c, "wslc image inspect") {
			continue
		}
		b.WriteString(c + "\n")
	}
	// Hashes embed absolute paths of the checkout; keep golden files portable.
	return hashRE.ReplaceAllString(b.String(), "config-hash=<hash>")
}

// container builds a list entry carrying compose labels.
func container(project, service string, n int, hash string, running bool) map[string]any {
	state := "exited"
	if running {
		state = "running"
	}
	l := (labels.Meta{Project: project}).Container(service, n, hash)
	return map[string]any{"Id": fmt.Sprintf("%s-%d", service, n), "Name": fmt.Sprintf("%s-%s-%d", project, service, n),
		"Image": "img", "State": state, "Labels": l}
}

type harness struct {
	app    *App
	fake   *fakeWSLC
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T, file string, mod func(*Options)) *harness {
	t.Helper()
	h := &harness{fake: &fakeWSLC{images: map[string]bool{}, health: map[string][]string{}, logs: map[string]string{}, exited: map[string]int{}}}
	o := Options{
		Files: []string{golden.Compose(file)}, IgnoreOSEnv: true, Bin: "wslc",
		DefaultDNS: []string{"1.1.1.1"}, PollEvery: time.Millisecond,
	}
	if mod != nil {
		mod(&o)
	}
	h.app = New(o, h.fake, golden.Paths, wslc.IO{Stdout: &h.stdout, Stderr: &h.stderr})
	return h
}

func hashOf(t *testing.T, h *harness, service string) string {
	t.Helper()
	var buf bytes.Buffer
	saved := h.app.io.Stdout
	h.app.io.Stdout = &buf
	defer func() { h.app.io.Stdout = saved }()
	if err := h.app.Config(context.Background(), ConfigOptions{Hash: service}); err != nil {
		t.Fatal(err)
	}
	_, hash, _ := strings.Cut(strings.TrimSpace(buf.String()), " ")
	return hash
}

// runnerFunc returns a Runner that always fails with err().
func runnerFunc(err func() error) wslc.Runner {
	return wslc.RunnerFunc(func(context.Context, []string, wslc.IO) error { return err() })
}
