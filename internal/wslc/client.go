package wslc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/DawnMagnet/wslc-compose-go/internal/labels"
)

// Options configures a Client.
type Options struct {
	Bin      string        // display name of the binary in dry-run output
	DryRun   bool          // print mutating commands instead of running them
	Log      io.Writer     // dry-run and retry messages (usually stderr)
	Parallel int           // max concurrent short-lived wslc calls (default 1)
	Retries  int           // attempts for transient Windows errors (default 5)
	Backoff  time.Duration // delay between retries (default 2s)
}

// Client is a typed facade over the wslc CLI. It serializes short calls
// (the wslc session store rejects heavily overlapping invocations), retries
// known transient errors, and implements --dry-run in one place.
type Client struct {
	r    Runner
	o    Options
	sem  chan struct{}
	warn sync.Once
}

// New returns a Client that executes through r.
func New(r Runner, o Options) *Client {
	if o.Parallel < 1 {
		o.Parallel = 1
	}
	if o.Retries < 1 {
		o.Retries = 5
	}
	if o.Backoff == 0 {
		o.Backoff = 2 * time.Second
	}
	if o.Bin == "" {
		o.Bin = "wslc"
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	return &Client{r: r, o: o, sem: make(chan struct{}, o.Parallel)}
}

// DryRun reports whether mutating calls are only printed.
func (c *Client) DryRun() bool { return c.o.DryRun }

// transient lists Windows error markers that clear up after a short delay:
// a just-stopped container's kernel object may still exist, and the session
// store can briefly refuse concurrent access.
var transient = []string{"ERROR_ALREADY_EXISTS", "ERROR_SHARING_VIOLATION"}

// network lists registry/network failure markers worth retrying for pulls
// (Docker Hub regularly drops connections with a bare EOF).
var network = []string{"EOF", "timeout", "connection reset", "TLS handshake", "temporarily unavailable", "i/o timeout", "503", "502", "429"}

func (c *Client) acquire() func() {
	c.sem <- struct{}{}
	return func() { <-c.sem }
}

// query runs a read-only command and returns stdout. In dry-run mode a
// failing query (typically: no wslc on this machine) yields empty output so
// that plans can still be printed.
func (c *Client) query(ctx context.Context, args ...string) ([]byte, error) {
	defer c.acquire()()
	var out bytes.Buffer
	err := c.r.Run(ctx, args, IO{Stdout: &out})
	if err != nil && c.o.DryRun {
		c.warn.Do(func() {
			fmt.Fprintf(c.o.Log, "# dry-run: cannot query wslc (%v); assuming empty state\n", err)
		})
		return nil, nil
	}
	return out.Bytes(), err
}

// mutate runs a state-changing command (or prints it in dry-run mode),
// retrying transient failures.
func (c *Client) mutate(ctx context.Context, stdio IO, args ...string) error {
	return c.retry(ctx, stdio, transient, args...)
}

// retry runs a mutation, retrying failures whose error text contains one of
// markers.
func (c *Client) retry(ctx context.Context, stdio IO, markers []string, args ...string) error {
	if c.o.DryRun {
		fmt.Fprintln(c.o.Log, Format(c.o.Bin, args))
		return nil
	}
	defer c.acquire()()
	var err error
	for attempt := 1; attempt <= c.o.Retries; attempt++ {
		if err = c.r.Run(ctx, args, stdio); err == nil || !matches(err, markers) || attempt == c.o.Retries {
			return err
		}
		fmt.Fprintf(c.o.Log, "wslc %s: transient error, retrying (%d/%d)\n", args[0], attempt, c.o.Retries-1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.o.Backoff):
		}
	}
	return err
}

// stream runs a long-lived command (logs, exec, attach) outside the
// concurrency limiter.
func (c *Client) stream(ctx context.Context, stdio IO, args ...string) error {
	if c.o.DryRun {
		fmt.Fprintln(c.o.Log, Format(c.o.Bin, args))
		return nil
	}
	return c.r.Run(ctx, args, stdio)
}

// matches reports whether err (including stderr that was already shown to
// the user) mentions one of markers.
func matches(err error, markers []string) bool {
	text := err.Error()
	var we *Error
	if errors.As(err, &we) {
		text += " " + we.Stderr
	}
	for _, m := range markers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// Containers lists a project's containers (all states when all is true).
// Entries whose list output lacks labels are enriched through inspect.
func (c *Client) Containers(ctx context.Context, project string, all bool) ([]Container, error) {
	args := []string{"list", "--format", "json", "--filter", labels.ProjectFilter(project)}
	if all {
		args = append(args, "--all")
	}
	out, err := c.query(ctx, args...)
	if err != nil {
		return nil, err
	}
	list, err := ParseContainers(out)
	if err != nil {
		return nil, err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.o.Parallel)
	for i := range list {
		if len(list[i].Labels) > 0 {
			continue
		}
		g.Go(func() error {
			full, err := c.Inspect(gctx, firstNonEmpty(list[i].ID, list[i].Name))
			if err == nil {
				list[i] = merge(list[i], full)
			}
			return nil
		})
	}
	_ = g.Wait()
	kept := list[:0]
	for _, ct := range list {
		if ct.Labels[labels.Project] == project {
			kept = append(kept, ct)
		}
	}
	return kept, nil
}

// Inspect returns the parsed inspect output for one container.
func (c *Client) Inspect(ctx context.Context, name string) (Container, error) {
	out, err := c.query(ctx, "inspect", name)
	if err != nil {
		return Container{}, err
	}
	list, err := ParseContainers(out)
	if err != nil || len(list) == 0 {
		return Container{}, fmt.Errorf("inspect %s: no data", name)
	}
	return list[0], nil
}

// ImageExists reports whether ref is present in the wslc image store.
// A missing image is an expected failure, so (unlike query) it never
// triggers the dry-run "cannot query wslc" notice.
func (c *Client) ImageExists(ctx context.Context, ref string) bool {
	defer c.acquire()()
	var out bytes.Buffer
	err := c.r.Run(ctx, []string{"image", "inspect", ref}, IO{Stdout: &out})
	return err == nil && len(bytes.TrimSpace(out.Bytes())) > 0
}

// Networks returns the set of existing network names.
func (c *Client) Networks(ctx context.Context) (map[string]bool, error) {
	return c.names(ctx, "network", "list")
}

// Volumes returns the set of existing volume names.
func (c *Client) Volumes(ctx context.Context) (map[string]bool, error) {
	return c.names(ctx, "volume", "list")
}

func (c *Client) names(ctx context.Context, args ...string) (map[string]bool, error) {
	out, err := c.query(ctx, args...)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, n := range parseNames(out) {
		set[n] = true
	}
	return set, nil
}

// NetworkSpec holds the options for `wslc network create`.
type NetworkSpec struct {
	Driver   string
	Internal bool
	Subnet   string
	Gateway  string
	Options  map[string]string
	Labels   map[string]string
}

// CreateNetwork creates a user-defined network.
func (c *Client) CreateNetwork(ctx context.Context, name string, s NetworkSpec) error {
	args := []string{"network", "create"}
	if s.Driver != "" && s.Driver != "bridge" {
		args = append(args, "--driver", s.Driver)
	}
	if s.Subnet != "" {
		args = append(args, "--subnet", s.Subnet)
	}
	if s.Gateway != "" {
		args = append(args, "--gateway", s.Gateway)
	}
	if s.Internal {
		args = append(args, "--internal")
	}
	for _, kv := range sortedPairs(s.Options) {
		args = append(args, "-o", kv)
	}
	args = append(args, labels.Flags(s.Labels)...)
	return c.mutate(ctx, IO{}, append(args, name)...)
}

// RemoveNetwork deletes a network.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	return c.mutate(ctx, IO{}, "network", "remove", name)
}

// CreateVolume creates a named volume carrying the given labels.
func (c *Client) CreateVolume(ctx context.Context, name string, l map[string]string) error {
	args := append([]string{"volume", "create"}, labels.Flags(l)...)
	return c.mutate(ctx, IO{}, append(args, name)...)
}

// RemoveVolume deletes a named volume.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	return c.mutate(ctx, IO{}, "volume", "remove", name)
}

// Run executes a prepared detached `run -d ...` argv (see package translate).
func (c *Client) Run(ctx context.Context, args []string) error {
	return c.mutate(ctx, IO{}, args...)
}

// RunAttached executes a prepared foreground `run ...` argv wired to stdio.
func (c *Client) RunAttached(ctx context.Context, args []string, stdio IO) error {
	return c.stream(ctx, stdio, args...)
}

// Build executes a prepared `build ...` argv, streaming progress.
func (c *Client) Build(ctx context.Context, args []string, stdio IO) error {
	return c.mutate(ctx, stdio, args...)
}

// Pull pulls an image, streaming progress; registry hiccups are retried.
func (c *Client) Pull(ctx context.Context, ref string, stdio IO) error {
	return c.retry(ctx, stdio, append(network, transient...), "pull", ref)
}

// Start starts an existing container.
func (c *Client) Start(ctx context.Context, name string) error {
	return c.mutate(ctx, IO{}, "start", name)
}

// Stop stops a container; timeout < 0 keeps the wslc default.
func (c *Client) Stop(ctx context.Context, name string, timeout int) error {
	args := []string{"stop"}
	if timeout >= 0 {
		args = append(args, "-t", strconv.Itoa(timeout))
	}
	return c.mutate(ctx, IO{}, append(args, name)...)
}

// Remove force-removes a container.
func (c *Client) Remove(ctx context.Context, name string) error {
	return c.mutate(ctx, IO{}, "remove", "-f", name)
}

// LogOptions mirrors the flags of `wslc logs`.
type LogOptions struct {
	Follow     bool
	Tail       string // "" or "all" means everything
	Timestamps bool
	Since      string
	Until      string
}

// Logs streams a container's logs to stdio.
func (c *Client) Logs(ctx context.Context, name string, o LogOptions, stdio IO) error {
	args := []string{"logs"}
	if o.Follow {
		args = append(args, "-f")
	}
	if o.Tail != "" && o.Tail != "all" {
		args = append(args, "-n", o.Tail)
	}
	if o.Timestamps {
		args = append(args, "-t")
	}
	if o.Since != "" {
		args = append(args, "--since", o.Since)
	}
	if o.Until != "" {
		args = append(args, "--until", o.Until)
	}
	return c.stream(ctx, stdio, append(args, name)...)
}

// Exec runs a prepared `exec ...` argv attached to stdio.
func (c *Client) Exec(ctx context.Context, args []string, stdio IO) error {
	return c.stream(ctx, stdio, args...)
}

// Version returns `wslc --version` output, or "" when unavailable.
func (c *Client) Version(ctx context.Context) string {
	var out bytes.Buffer
	if err := c.r.Run(ctx, []string{"--version"}, IO{Stdout: &out}); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// Format renders a command line for display, quoting where needed.
func Format(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, a := range append([]string{bin}, args...) {
		if a == "" || strings.ContainsAny(a, " \t\"'$`\\|&;<>()*?") {
			a = strconv.Quote(a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func merge(base, full Container) Container {
	if full.ID == "" {
		full.ID = base.ID
	}
	if full.Name == "" {
		full.Name = base.Name
	}
	if full.Image == "" {
		full.Image = base.Image
	}
	if full.State == "" {
		full.State = base.State
	}
	if full.Ports == "" {
		full.Ports = base.Ports
	}
	return full
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func sortedPairs(m map[string]string) []string {
	pairs := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		pairs = append(pairs, k+"="+m[k])
	}
	return pairs
}
