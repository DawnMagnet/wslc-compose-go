package wslc

import "testing"

func TestParseRuntime(t *testing.T) {
	raw := []byte(`[{
	  "HostConfig": {"NetworkMode": "proj_front"},
	  "NetworkSettings": {"Networks": {"proj_front": {"IPAddress": "172.28.0.2", "Gateway": "172.28.0.1", "Aliases": ["web"]}}},
	  "Ports": {"80/tcp": [{"HostIp": "0.0.0.0", "HostPort": "8080"}]}
	}]`)
	rt := ParseRuntime(raw)
	if rt.Primary != "proj_front" || len(rt.Endpoints) != 1 || rt.Endpoints[0].IP != "172.28.0.2" || rt.Endpoints[0].Gateway != "172.28.0.1" {
		t.Fatalf("endpoints: %+v", rt)
	}
	if len(rt.Ports) != 1 || rt.Ports[0].Target != 80 || rt.Ports[0].HostPort != 8080 || rt.Ports[0].Proto != "tcp" {
		t.Fatalf("ports: %+v", rt.Ports)
	}
	if rt := ParseRuntime([]byte("not json")); len(rt.Endpoints) != 0 {
		t.Fatalf("bad json should degrade: %+v", rt)
	}
}
