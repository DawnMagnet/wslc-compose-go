package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DawnMagnet/wslc-compose-go/internal/golden"
	"github.com/DawnMagnet/wslc-compose-go/internal/labels"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

// fakeWSLC emulates just enough of the wslc CLI for orchestration tests.
type fakeWSLC struct {
	mu           sync.Mutex
	calls        []string
	containers   []map[string]any
	networks     []string
	volumes      []string
	images       map[string]bool
	health       map[string][]string // per-container sequence of health states
	logs         map[string]string
	exited       map[string]int // containers that report exited with this code
	failRun      string         // `run` of this container name fails
	failBuild    bool           // `build` fails
	failSession  bool           // `system session run` fails
	sessionFlaky bool           // script runs, wslc still returns ERROR_INVALID_HANDLE
	sessionCode  int            // exit status recorded by the script when sessionFlaky
	lastSession  string
	recorded     string
	version      string                    // `wslc --version` text; empty means 3.0.3.0
	runtime      map[string]map[string]any // extra inspect fields keyed by container name
	track        bool                      // record containers created by run/create
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
		for _, n := range f.networks {
			fmt.Fprintf(out, "{\"Driver\":\"bridge\",\"ID\":\"x\",\"Name\":%q}\n", n)
		}
		return nil
	case "volume list":
		for _, v := range f.volumes {
			fmt.Fprintf(out, "{\"Driver\":\"guest\",\"Name\":%q}\n", v)
		}
		return nil
	case "image inspect":
		if f.images[args[2]] {
			fmt.Fprintln(out, `[{"Id":"sha256:1"}]`)
			return nil
		}
		return errors.New("no such image")
	}
	if args[0] == "run" && f.failRun != "" && slices.Contains(args, f.failRun) {
		return errors.New("invalid volume spec")
	}
	if args[0] == "build" && f.failBuild {
		return errors.New("build failed")
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
		obj := map[string]any{"Name": name, "State": state}
		for k, v := range f.runtime[name] {
			obj[k] = v
		}
		return json.NewEncoder(out).Encode([]any{obj})
	case "logs":
		fmt.Fprint(out, f.logs[args[len(args)-1]])
	case "--version", "version":
		v := f.version
		if v == "" {
			v = "wslc version 3.0.3.0"
		}
		fmt.Fprintln(out, v)
	}
	if len(args) >= 2 && args[0] == "system" && args[1] == "session" {
		script := args[len(args)-1]
		if f.sessionFlaky {
			if strings.HasPrefix(script, "cat ") {
				path := strings.TrimSpace(strings.TrimPrefix(script, "cat "))
				if f.recorded == "" || !strings.Contains(f.lastSession, "'"+path+"'") {
					return &wslc.Error{Args: args, Code: 1, Stderr: "cat: " + path + ": No such file or directory"}
				}
				fmt.Fprint(out, f.recorded)
				return nil
			}
			nonce := between(script, "_wcg_nonce='", "'")
			status := between(script, "_wcg_status='", "'")
			if nonce == "" || status == "" {
				return errors.New("session script has no status trap")
			}
			f.lastSession = script
			f.recorded = fmt.Sprintf("%s %d\n", nonce, f.sessionCode)
			return &wslc.Error{Args: args, Code: 1, Stderr: "句柄无效。 错误代码： ERROR_INVALID_HANDLE"}
		}
		if f.failSession {
			return errors.New("iptables failed")
		}
	}
	f.observe(args)
	return nil
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// observe updates the fake's view of containers, networks, volumes and images
// after a successful mutation so a later command in the same test sees them.
func (f *fakeWSLC) observe(args []string) {
	switch args[0] {
	case "run", "create":
		f.remember(args)
	case "remove":
		f.forget(args[len(args)-1])
	case "pull":
		if len(args) > 1 {
			if f.images == nil {
				f.images = map[string]bool{}
			}
			f.images[args[1]] = true
		}
	case "build":
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-t" {
				if f.images == nil {
					f.images = map[string]bool{}
				}
				f.images[args[i+1]] = true
			}
		}
	case "network":
		if len(args) < 3 {
			return
		}
		name := args[len(args)-1]
		switch args[1] {
		case "create":
			f.networks = append(f.networks, name)
		case "remove":
			f.networks = slices.DeleteFunc(f.networks, func(n string) bool { return n == name })
		}
	case "volume":
		if len(args) < 3 {
			return
		}
		name := args[len(args)-1]
		switch args[1] {
		case "create":
			f.volumes = append(f.volumes, name)
		case "remove":
			f.volumes = slices.DeleteFunc(f.volumes, func(n string) bool { return n == name })
		}
	}
}

func (f *fakeWSLC) forget(name string) {
	f.containers = slices.DeleteFunc(f.containers, func(m map[string]any) bool {
		n, _ := m["Name"].(string)
		id, _ := m["Id"].(string)
		return n == name || id == name
	})
}

// remember keeps a container created by run/create so a later list/inspect
// in the same test can see it (host-gateway programming reads inspect).
func (f *fakeWSLC) remember(args []string) {
	name := ""
	labs := map[string]string{}
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--name":
			name = args[i+1]
		case "-l":
			if k, v, ok := strings.Cut(args[i+1], "="); ok {
				labs[k] = v
			}
		}
	}
	if name == "" {
		return
	}
	f.containers = append(f.containers, map[string]any{
		"Id": name, "Name": name, "State": "running", "Labels": labs,
	})
}

var (
	hashRE  = regexp.MustCompile(`config-hash=[0-9a-f]{64}`)
	nonceRE = regexp.MustCompile(`_wcg_nonce='[0-9a-f]+'`)
)

// scrub makes command output stable: config hashes embed absolute paths, and
// each session invocation carries a random nonce.
func scrub(s string) string {
	s = hashRE.ReplaceAllString(s, "config-hash=<hash>")
	return nonceRE.ReplaceAllString(s, `_wcg_nonce='<nonce>'`)
}

// mutations returns recorded calls that change state (queries filtered out).
func (f *fakeWSLC) mutations() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for _, c := range f.calls {
		if strings.HasPrefix(c, "wslc list") || strings.HasPrefix(c, "wslc inspect") ||
			strings.Contains(c, " list") || strings.HasPrefix(c, "wslc image inspect") ||
			strings.HasPrefix(c, "wslc --version") || strings.HasPrefix(c, "wslc version") {
			continue
		}
		b.WriteString(c + "\n")
	}
	return scrub(b.String())
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

// hostGatewayContainer is container plus the label written when rules may
// have been programmed.
func hostGatewayContainer(project, service string, n int, hash string, running bool) map[string]any {
	c := container(project, service, n, hash, running)
	c["Labels"].(map[string]string)[labels.HostGateway] = "v1"
	return c
}

type harness struct {
	app    *App
	fake   *fakeWSLC
	paths  func(string) string
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T, file string, mod func(*Options)) *harness {
	t.Helper()
	h := &harness{fake: &fakeWSLC{images: map[string]bool{}, health: map[string][]string{}, logs: map[string]string{}, exited: map[string]int{}}}
	stateDir := t.TempDir()
	o := Options{
		Files: []string{golden.Compose(file)}, IgnoreOSEnv: true, Bin: "wslc",
		DefaultDNS: []string{"1.1.1.1"}, PollEvery: time.Millisecond,
		StateDir: stateDir,
	}
	if mod != nil {
		mod(&o)
	}
	mapPath := func(p string) string {
		slash := filepath.ToSlash(p)
		root := filepath.ToSlash(stateDir)
		if strings.HasPrefix(slash, root) {
			return "C:/state" + strings.TrimPrefix(slash, root)
		}
		return golden.Paths(p)
	}
	h.paths = mapPath
	h.app = New(o, h.fake, mapPath, wslc.IO{Stdout: &h.stdout, Stderr: &h.stderr})
	return h
}

// reopen rebuilds the App so a new wslc version probe is taken. The fake's
// containers and resources are kept.
func (h *harness) reopen() {
	h.stdout.Reset()
	h.stderr.Reset()
	h.app = New(h.app.o, h.fake, h.paths, wslc.IO{Stdout: &h.stdout, Stderr: &h.stderr})
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
