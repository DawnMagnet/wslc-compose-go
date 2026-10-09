package translate

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/golden"
	"github.com/DawnMagnet/wslc-compose-go/internal/labels"
	"github.com/DawnMagnet/wslc-compose-go/internal/project"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

func translator(t *testing.T, file string) *Translator {
	t.Helper()
	p, err := project.Load(context.Background(), project.Options{Files: []string{golden.Compose(file)}, IgnoreOSEnv: true})
	if err != nil {
		t.Fatal(err)
	}
	return &Translator{Project: p, Paths: golden.Paths, DefaultDNS: DefaultDNS,
		Meta: labels.Meta{Project: p.Name, Version: "test"}}
}

// TestGoldenArgv locks the exact wslc argv generated for every testdata service.
func TestGoldenArgv(t *testing.T) {
	for _, file := range []string{"full.yaml", "minimal.yaml", "profiles.yaml"} {
		t.Run(file, func(t *testing.T) {
			tr := translator(t, file)
			var b strings.Builder
			for _, name := range tr.Project.ServiceNames() {
				s := tr.Project.Services[name]
				if args := tr.Build(s, false, false); args != nil {
					fmt.Fprintf(&b, "[%s] %s\n", name, wslc.Format("wslc", args))
				}
				args := tr.Run(s, RunOptions{Number: 1, Hash: "HASH", Detach: true, Publish: true})
				fmt.Fprintf(&b, "[%s] %s\n", name, wslc.Format("wslc", args))
			}
			golden.Assert(t, "translate_"+strings.TrimSuffix(file, ".yaml")+".golden", b.String())
		})
	}
}

func ptr[T any](v T) *T { return &v }

func dur(d time.Duration) *types.Duration { return ptr(types.Duration(d)) }

func TestPorts(t *testing.T) {
	tests := []struct {
		in   types.ServicePortConfig
		want string
	}{
		{types.ServicePortConfig{Target: 80}, "80"},
		{types.ServicePortConfig{Target: 80, Published: "8080", Protocol: "tcp"}, "8080:80"},
		{types.ServicePortConfig{Target: 53, Published: "53", Protocol: "udp", HostIP: "127.0.0.1"}, "127.0.0.1:53:53/udp"},
		{types.ServicePortConfig{Target: 443, HostIP: "0.0.0.0"}, "443"},
	}
	for _, tt := range tests {
		if got := Ports([]types.ServicePortConfig{tt.in}); got[0] != tt.want {
			t.Errorf("Ports(%+v) = %q, want %q", tt.in, got[0], tt.want)
		}
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{
		512 << 20: "512M", 1 << 30: "1G", 3 << 29: "1536M", 64 << 10: "64K", 1000: "1000",
	} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name string
		in   *types.HealthCheckConfig
		want []string
	}{
		{"nil", nil, nil},
		{"disabled", &types.HealthCheckConfig{Disable: true}, []string{"--no-healthcheck"}},
		{"NONE", &types.HealthCheckConfig{Test: []string{"NONE"}}, []string{"--no-healthcheck"}},
		{"shell", &types.HealthCheckConfig{Test: []string{"CMD-SHELL", "pg_isready -U x"}, Retries: ptr[uint64](3)},
			[]string{"--health-cmd", "pg_isready -U x", "--health-retries", "3"}},
		{"exec form", &types.HealthCheckConfig{
			Test: []string{"CMD", "wget", "-qO-", "http://x/a b"}, Interval: dur(90 * time.Second),
			Timeout: dur(time.Second), StartPeriod: dur(5 * time.Second),
		}, []string{"--health-cmd", "wget -qO- 'http://x/a b'", "--health-interval", "1m30s",
			"--health-timeout", "1s", "--health-start-period", "5s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Health(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestShellJoin(t *testing.T) {
	got := ShellJoin([]string{"echo", "it's", "", "$HOME", "plain"})
	want := `echo 'it'\''s' '' '$HOME' plain`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestEnvSkipsUnset(t *testing.T) {
	got := Env(types.MappingWithEquals{"B": ptr("2"), "A": ptr(""), "UNSET": nil})
	if !reflect.DeepEqual(got, []string{"A=", "B=2"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRunVariants(t *testing.T) {
	p := &types.Project{Name: "p", Networks: types.Networks{"default": {Name: "p_default"}}}
	tr := &Translator{Project: p}
	s := types.ServiceConfig{Name: "svc", ContainerSpec: types.ContainerSpec{
		Image: "alpine", Command: []string{"sleep", "1"}, Networks: map[string]*types.ServiceNetworkConfig{"default": nil},
	}, WorkloadSpec: types.WorkloadSpec{Tty: true, Ports: []types.ServicePortConfig{{Target: 80}}}}

	tests := []struct {
		name string
		opts RunOptions
		svc  func(*types.ServiceConfig)
		want string
	}{
		{"detached replica", RunOptions{Number: 2, Detach: true, Publish: true}, nil,
			"run -d --name p-svc-2 -p 80 --network p_default --network-alias svc -t alpine sleep 1"},
		{"one-off override", RunOptions{Name: "x", Remove: true, Command: []string{"sh"}, TTY: true, ExtraEnv: []string{"K=V"}}, nil,
			"run --rm --name x -e K=V --network p_default -i -t alpine sh"},
		{"no tty", RunOptions{Number: 1, NoTTY: true}, nil,
			"run --name p-svc-1 --network p_default --network-alias svc alpine sleep 1"},
		{"network none", RunOptions{Number: 1}, func(s *types.ServiceConfig) { s.NetworkMode = "none" },
			"run --name p-svc-1 --network none -t alpine sleep 1"},
		{"network host dropped", RunOptions{Number: 1}, func(s *types.ServiceConfig) { s.NetworkMode = "host" },
			"run --name p-svc-1 -t alpine sleep 1"},
		{"empty command clears", RunOptions{Number: 1, Command: []string{}}, nil,
			"run --name p-svc-1 --network p_default --network-alias svc -t alpine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := s
			if tt.svc != nil {
				tt.svc(&svc)
			}
			got := tr.Run(svc, tt.opts)
			// drop label flags to keep expectations readable
			var kept []string
			for i := 0; i < len(got); i++ {
				if got[i] == "-l" {
					i++
					continue
				}
				kept = append(kept, got[i])
			}
			if g := strings.Join(kept, " "); g != tt.want {
				t.Fatalf("got  %s\nwant %s", g, tt.want)
			}
		})
	}
}

func TestBuildDockerfileResolution(t *testing.T) {
	p := &types.Project{Name: "p"}
	tr := &Translator{Project: p, Paths: func(s string) string { return "W" + filepath.ToSlash(s) }}
	s := types.ServiceConfig{Name: "app", WorkloadSpec: types.WorkloadSpec{Build: &types.BuildConfig{
		Context: "/src/app", Dockerfile: "docker/Dockerfile", NoCache: true, Tags: []string{"extra:1"},
	}}}
	got := strings.Join(tr.Build(s, false, true), " ")
	want := "build -t p-app -t extra:1 -f W/src/app/docker/Dockerfile --pull --no-cache W/src/app"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if tr.Build(types.ServiceConfig{Name: "x"}, false, false) != nil {
		t.Fatal("services without build must return nil")
	}
}

func TestNames(t *testing.T) {
	p := &types.Project{Name: "demo"}
	s := types.ServiceConfig{Name: "web"}
	if ImageName(p, s) != "demo-web" || ContainerName(p, s, 3) != "demo-web-3" {
		t.Fatal("generated names wrong")
	}
	s.Image, s.ContainerName = "nginx", "custom"
	if ImageName(p, s) != "nginx" || ContainerName(p, s, 1) != "custom" {
		t.Fatal("explicit names ignored")
	}
}
