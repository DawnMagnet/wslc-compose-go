package translate

import (
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestExtraHostsScript(t *testing.T) {
	script, err := ExtraHostsScript(types.HostsList{
		"db.internal":          {"10.1.2.3"},
		"host.docker.internal": {"host-gateway"},
		"b.example":            {"2001:db8::1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/proc/net/route",
		"wslc-compose-extra-host",
		"printf '%s db.internal",
		"'10.1.2.3'",
		"printf '%s host.docker.internal",
		"$GW",
		"grep -v",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\n%s", want, script)
		}
	}
	// Hostnames are emitted in sorted order so the script is stable.
	if strings.Index(script, "b.example") > strings.Index(script, "db.internal") {
		t.Fatalf("hosts not sorted:\n%s", script)
	}
	if _, err := ExtraHostsScript(types.HostsList{"bad host": {"10.0.0.1"}}); err == nil {
		t.Fatal("hostname with a space must be rejected")
	}
	if _, err := ExtraHostsScript(types.HostsList{"ok": {"not-an-ip"}}); err == nil {
		t.Fatal("bad address must be rejected")
	}
}

func TestGatewayScriptAndRoutes(t *testing.T) {
	services := []GatewayService{{
		Name: "web", Networks: []string{"hosts_front"}, Gateway: true,
		Ports: []types.ServicePortConfig{{Target: 80, Published: "8080", Protocol: "tcp"}},
	}}
	live := []LiveContainer{{
		Name: "hosts-web-1", Service: "web", Running: true, Primary: "hosts_front",
		Endpoints: []LiveEndpoint{{Network: "hosts_front", IP: "172.28.0.2", Gateway: "172.28.0.1"}},
		Ports:     []LivePort{{Target: 80, Proto: "tcp", HostPort: 8080, HostIP: "0.0.0.0"}},
	}}
	routes, err := CollectRoutes(services, live)
	if err != nil || len(routes) != 1 {
		t.Fatalf("routes: %+v %v", routes, err)
	}
	script, err := GatewayScript("hosts", routes, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"WCGP_", "WCGS_", "WCGF_",
		"-d 172.28.0.1/32 -p tcp --dport 8080 -j DNAT --to-destination 172.28.0.2:80",
		"MASQUERADE",
		"DOCKER-USER",
		"iptables-restore --wait 10 --noflush",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install script missing %q", want)
		}
	}
	cleanup, err := GatewayScript("hosts", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cleanup, "DNAT") || strings.Contains(cleanup, "WCG_RULES") {
		t.Fatalf("cleanup must not install rules:\n%s", cleanup)
	}
	if !strings.Contains(cleanup, "-X ") {
		t.Fatalf("cleanup must delete chains:\n%s", cleanup)
	}
	// Loopback publications are not widened.
	services[0].Ports = []types.ServicePortConfig{{Target: 80, Published: "8080", HostIP: "127.0.0.1"}}
	routes, err = CollectRoutes(services, live)
	if err != nil || len(routes) != 0 {
		t.Fatalf("loopback must be skipped: %+v %v", routes, err)
	}
}
