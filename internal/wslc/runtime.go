package wslc

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Endpoint is one network attachment reported by inspect.
type Endpoint struct {
	Network string
	IP      string
	Gateway string
	Aliases []string
}

// Published is one host port binding reported by inspect.
type Published struct {
	Target   int
	Proto    string
	HostPort int
	HostIP   string
}

// Runtime is the inspect subset used for host-gateway rules and endpoint checks.
type Runtime struct {
	Primary   string
	Endpoints []Endpoint
	Ports     []Published
}

// ParseRuntime reads Docker-like or wslc inspect JSON. Unknown shapes yield
// a zero Runtime rather than an error so a missing field degrades to "no
// routes" instead of failing up.
func ParseRuntime(data []byte) Runtime {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return Runtime{}
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return Runtime{}
	}
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			return Runtime{}
		}
		if m, ok := t[0].(map[string]any); ok {
			return runtimeFrom(m)
		}
	case map[string]any:
		return runtimeFrom(t)
	}
	return Runtime{}
}

func runtimeFrom(m map[string]any) Runtime {
	var rt Runtime
	rt.Primary = str(find(m, "HostConfig", "NetworkMode"))
	if nets, ok := find(m, "NetworkSettings", "Networks").(map[string]any); ok {
		for name, raw := range nets {
			ep, _ := raw.(map[string]any)
			if ep == nil {
				continue
			}
			rt.Endpoints = append(rt.Endpoints, Endpoint{
				Network: name,
				IP:      str(find(ep, "IPAddress")),
				Gateway: str(find(ep, "Gateway")),
				Aliases: stringList(find(ep, "Aliases")),
			})
		}
	}
	switch ports := find(m, "Ports").(type) {
	case map[string]any:
		for key, raw := range ports {
			target, proto := splitPortKey(key)
			for _, binding := range anyList(raw) {
				rt.Ports = append(rt.Ports, Published{
					Target:   target,
					Proto:    proto,
					HostPort: atoi(str(find(binding, "HostPort"))),
					HostIP:   str(find(binding, "HostIp"), find(binding, "HostIP")),
				})
			}
		}
	case []any:
		for _, raw := range ports {
			b, _ := raw.(map[string]any)
			if b == nil {
				continue
			}
			proto := strings.ToLower(str(find(b, "Protocol"), find(b, "Type")))
			if proto == "" || proto == "6" {
				proto = "tcp"
			}
			if proto == "17" {
				proto = "udp"
			}
			rt.Ports = append(rt.Ports, Published{
				Target:   atoi(str(find(b, "ContainerPort"), find(b, "PrivatePort"))),
				Proto:    proto,
				HostPort: atoi(str(find(b, "HostPort"), find(b, "PublicPort"))),
				HostIP:   str(find(b, "HostIp"), find(b, "BindingAddress"), find(b, "IP")),
			})
		}
	}
	return rt
}

func splitPortKey(key string) (int, string) {
	target, proto, ok := strings.Cut(key, "/")
	if !ok {
		return atoi(key), "tcp"
	}
	proto = strings.ToLower(proto)
	if proto == "" {
		proto = "tcp"
	}
	return atoi(target), proto
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func anyList(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
