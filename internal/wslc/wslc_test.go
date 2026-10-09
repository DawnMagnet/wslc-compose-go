package wslc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseContainers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Container
	}{
		{"docker-like list", `[{"Id":"a1","Names":["/p-web-1"],"Image":"nginx","State":"running","Labels":"com.docker.compose.project=p,com.docker.compose.service=web"}]`,
			[]Container{{ID: "a1", Name: "p-web-1", Image: "nginx", State: "running", Labels: map[string]string{"com.docker.compose.project": "p", "com.docker.compose.service": "web"}}}},
		{"oci inspect object", `{"id":"b2","name":"p-db-1","config":{"image":"pg","labels":{"com.docker.compose.container-number":"1"}},"state":{"status":"Exited","exitCode":3,"health":{"status":"Unhealthy"}}}`,
			[]Container{{ID: "b2", Name: "p-db-1", Image: "pg", State: "exited", Health: "unhealthy", ExitCode: 3, Labels: map[string]string{"com.docker.compose.container-number": "1"}}}},
		{"ndjson + running bool", "{\"Name\":\"x\",\"State\":{\"Running\":true}}\n{\"Name\":\"y\",\"State\":{\"Running\":false}}\n",
			[]Container{{Name: "x", State: "running", Labels: map[string]string{}}, {Name: "y", State: "exited", Labels: map[string]string{}}}},
		{"status text", `[{"Name":"z","Status":"Up 3 minutes","Ports":[{"HostPort":8080,"ContainerPort":80,"Protocol":"tcp"}]}]`,
			[]Container{{Name: "z", State: "running", Ports: "0.0.0.0:8080->80/tcp", Labels: map[string]string{}}}},
		{"empty", "  \n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseContainers([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
	if _, err := ParseContainers([]byte("[not json")); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestContainerAccessors(t *testing.T) {
	c := Container{State: "running", Labels: map[string]string{
		"com.docker.compose.service": "web", "com.docker.compose.container-number": "2",
		"com.wslc.compose.config-hash": "h", "com.docker.compose.oneoff": "True",
	}}
	if !c.Running() || c.Service() != "web" || c.Number() != 2 || c.Hash() != "h" || !c.OneOff() {
		t.Fatalf("accessors wrong: %+v", c)
	}
}

func TestParseNames(t *testing.T) {
	table := "NETWORK ID     NAME          DRIVER\r\nabc            p_default     bridge\r\ndef            other         bridge\r\n"
	if got := parseNames([]byte(table)); !reflect.DeepEqual(got, []string{"p_default", "other"}) {
		t.Fatalf("table: %v", got)
	}
	vol := "DRIVER    VOLUME NAME\nlocal     p_data\n"
	if got := parseNames([]byte(vol)); !reflect.DeepEqual(got, []string{"p_data"}) {
		t.Fatalf("volume table: %v", got)
	}
	if got := parseNames([]byte(`[{"Name":"a"},{"name":"b"}]`)); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("json: %v", got)
	}
}

type recorder struct {
	calls   []string
	respond func(args []string, stdio IO) error
}

func (r *recorder) Run(_ context.Context, args []string, stdio IO) error {
	r.calls = append(r.calls, strings.Join(args, " "))
	if r.respond != nil {
		return r.respond(args, stdio)
	}
	return nil
}

func TestDryRunPrintsMutations(t *testing.T) {
	var log bytes.Buffer
	r := &recorder{respond: func([]string, IO) error { return errors.New("no wslc") }}
	c := New(r, Options{Bin: "wslc.exe", DryRun: true, Log: &log})
	ctx := context.Background()
	if err := c.Run(ctx, []string{"run", "-d", "--name", "x", "-e", "A=b c", "img"}); err != nil {
		t.Fatal(err)
	}
	_ = c.Stop(ctx, "x", 5)
	nets, err := c.Networks(ctx)
	if err != nil || len(nets) != 0 {
		t.Fatalf("dry-run query should degrade to empty: %v %v", nets, err)
	}
	if len(r.calls) != 1 || r.calls[0] != "network list" {
		t.Fatalf("dry-run must only execute queries, got %v", r.calls)
	}
	out := log.String()
	for _, want := range []string{`wslc.exe run -d --name x -e "A=b c" img`, "wslc.exe stop -t 5 x", "assuming empty state"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestRetryTransient(t *testing.T) {
	n := 0
	r := &recorder{respond: func([]string, IO) error {
		if n++; n < 3 {
			return &Error{Args: []string{"start"}, Code: 1, Stderr: "HRESULT ERROR_SHARING_VIOLATION"}
		}
		return nil
	}}
	c := New(r, Options{Backoff: time.Millisecond, Log: io.Discard})
	if err := c.Start(context.Background(), "x"); err != nil || n != 3 {
		t.Fatalf("expected success after 3 attempts, got %v after %d", err, n)
	}
	n = 0
	r.respond = func([]string, IO) error { n++; return errors.New("permanent") }
	if err := c.Start(context.Background(), "x"); err == nil || n != 1 {
		t.Fatalf("non-transient errors must not retry (n=%d)", n)
	}
}

func TestContainersEnrichAndFilter(t *testing.T) {
	r := &recorder{respond: func(args []string, stdio IO) error {
		switch args[0] {
		case "list":
			io.WriteString(stdio.Stdout, `[{"Id":"1","Name":"p-web-1","State":"running"},{"Id":"2","Name":"q-x-1","Labels":{"com.docker.compose.project":"q"}}]`)
		case "inspect":
			io.WriteString(stdio.Stdout, `[{"Id":"1","Config":{"Labels":{"com.docker.compose.project":"p","com.docker.compose.service":"web"}},"State":{"Status":"running"}}]`)
		}
		return nil
	}}
	cs, err := New(r, Options{}).Containers(context.Background(), "p", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Name != "p-web-1" || cs[0].Service() != "web" {
		t.Fatalf("got %+v", cs)
	}
	if r.calls[0] != "list --format json --filter label=com.docker.compose.project=p --all" || r.calls[1] != "inspect 1" {
		t.Fatalf("calls %v", r.calls)
	}
}

func TestLogsAndCreateArgs(t *testing.T) {
	r := &recorder{}
	c := New(r, Options{})
	ctx := context.Background()
	_ = c.Logs(ctx, "x", LogOptions{Follow: true, Tail: "10", Timestamps: true, Since: "1h"}, IO{})
	_ = c.Logs(ctx, "x", LogOptions{Tail: "all"}, IO{})
	_ = c.CreateNetwork(ctx, "n", NetworkSpec{Driver: "bridge", Internal: true, Options: map[string]string{"b": "2", "a": "1"}, Labels: map[string]string{"k": "v"}})
	_ = c.Remove(ctx, "x")
	want := []string{
		"logs -f -n 10 -t --since 1h x",
		"logs x",
		"network create --internal -o a=1 -o b=2 -l k=v n",
		"remove -f x",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("got %v", r.calls)
	}
}

func TestErrorAndExitCode(t *testing.T) {
	err := &Error{Args: []string{"run", "-d", "x"}, Code: 125, Stderr: "boom"}
	if err.Error() != "wslc run -d: exit 125: boom" || ExitCode(err) != 125 || ExitCode(nil) != 0 || ExitCode(errors.New("x")) != 1 {
		t.Fatalf("unexpected: %s", err)
	}
	if Find("C:/explicit/wslc.exe") != "C:/explicit/wslc.exe" {
		t.Fatal("explicit binary ignored")
	}
	t.Setenv("WSLC_COMPOSE_BIN", "/env/wslc")
	if Find("") != "/env/wslc" {
		t.Fatal("env binary ignored")
	}
}

func TestExecRunnerReportsFailures(t *testing.T) {
	err := Exec{Bin: "/nonexistent/wslc"}.Run(context.Background(), []string{"list"}, IO{})
	var we *Error
	if !errors.As(err, &we) || we.Code != -1 {
		t.Fatalf("expected *Error with code -1, got %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := (Exec{Bin: "sh"}).Run(context.Background(), []string{"-c", "echo out; echo err >&2; exit 3"}, IO{}); ExitCode(err) != 3 || !strings.Contains(err.Error(), "err") {
		t.Fatalf("exit code/stderr not captured: %v", err)
	}
}
