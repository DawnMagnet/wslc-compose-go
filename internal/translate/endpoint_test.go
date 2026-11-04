package translate

import (
	"context"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/golden"
	"github.com/DawnMagnet/wslc-compose-go/internal/project"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

func TestConnectAndCreate(t *testing.T) {
	tr := translator(t, "networks.yaml")
	app := tr.Project.Services["app"]
	if got := app.NetworksByPriority(); len(got) != 2 || got[0] != "back" || got[1] != "front" {
		t.Fatalf("priority order: %v", got)
	}
	create := tr.Create(app, RunOptions{Number: 1, Hash: "HASH", Publish: true})
	if create[0] != "create" || contains(create, "-d") {
		t.Fatalf("create argv: %v", create)
	}
	line := wslc.Format("wslc", create)
	for _, want := range []string{"--network nets_back", "--ip 172.28.1.10", "--network-alias app", "--network-alias db"} {
		if !strings.Contains(line, want) {
			t.Errorf("create missing %q in %s", want, line)
		}
	}
	if strings.Contains(line, "nets_front") || strings.Contains(line, "--link-local-ip") {
		t.Fatalf("primary argv must not carry the secondary endpoint: %s", line)
	}
	conn := wslc.Format("wslc", tr.Connect(app, "front", "nets-app-1"))
	for _, want := range []string{
		"network connect", "--ip 172.28.0.10", "--link-local-ip 169.254.1.10",
		"--driver-opt com.docker.network.endpoint.foo=bar", "--network-alias app",
		"--network-alias www", "nets_front nets-app-1",
	} {
		if !strings.Contains(conn, want) {
			t.Errorf("connect missing %q in %s", want, conn)
		}
	}
}

func TestValidateServiceNetworks(t *testing.T) {
	p, err := project.Load(context.Background(), project.Options{Files: []string{golden.Compose("badnet.yaml")}, IgnoreOSEnv: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateServiceNetworks(p, true); err == nil || !strings.Contains(err.Error(), "not a usable") {
		t.Fatalf("network address: %v", err)
	}
	if _, err := ValidateServiceNetworks(p, false); err != nil {
		t.Fatalf("old wslc must not reject a static address: %v", err)
	}

	a := types.ServiceConfig{Name: "a"}
	a.Networks = map[string]*types.ServiceNetworkConfig{"n": {Ipv4Address: "10.0.0.10"}}
	b := types.ServiceConfig{Name: "b"}
	b.Networks = map[string]*types.ServiceNetworkConfig{"n": {Ipv4Address: "10.0.0.10"}}
	dup := &types.Project{
		Services: types.Services{"a": a, "b": b},
		Networks: types.Networks{"n": {Ipam: types.IPAMConfig{Config: []*types.IPAMPool{{Subnet: "10.0.0.0/24", Gateway: "10.0.0.1"}}}}},
	}
	if _, err := ValidateServiceNetworks(dup, true); err == nil || !strings.Contains(err.Error(), "also requested") {
		t.Fatalf("duplicate: %v", err)
	}
	two := 2
	b.Scale = &two
	b.Networks = map[string]*types.ServiceNetworkConfig{"n": {Ipv4Address: "10.0.0.11"}}
	dup.Services["b"] = b
	if _, err := ValidateServiceNetworks(dup, true); err == nil || !strings.Contains(err.Error(), "replicas") {
		t.Fatalf("scale: %v", err)
	}
	lone := types.ServiceConfig{Name: "a"}
	badRange := &types.Project{
		Services: types.Services{"a": lone},
		Networks: types.Networks{"n": {Ipam: types.IPAMConfig{Config: []*types.IPAMPool{{Subnet: "10.0.0.0/24", IPRange: "10.1.0.0/24"}}}}},
	}
	if _, err := ValidateServiceNetworks(badRange, true); err == nil || !strings.Contains(err.Error(), "ip_range") {
		t.Fatalf("ip_range: %v", err)
	}
	if warns, err := ValidateServiceNetworks(badRange, false); err != nil || len(warns) == 0 || !strings.Contains(warns[0], "ip_range") {
		t.Fatalf("old wslc should warn, not fail: warns=%v err=%v", warns, err)
	}
	gwSvc := types.ServiceConfig{Name: "a"}
	gwSvc.Networks = map[string]*types.ServiceNetworkConfig{"n": {Ipv4Address: "10.0.0.1"}}
	gw := &types.Project{
		Services: types.Services{"a": gwSvc},
		Networks: types.Networks{"n": {Ipam: types.IPAMConfig{Config: []*types.IPAMPool{{Subnet: "10.0.0.0/24", Gateway: "10.0.0.1"}}}}},
	}
	if _, err := ValidateServiceNetworks(gw, true); err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("gateway address: %v", err)
	}
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
