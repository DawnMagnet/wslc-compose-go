package translate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// Route is one published-port hairpin: traffic to gateway:HostPort is
// redirected to the container's Address:Port. Chain names and rule shape
// follow the host-gateway compatibility protocol used by
// bacarndiaye/wslc-compose (MIT) so two frontends can reconcile one project.
type Route struct {
	Gateway  string
	Proto    string
	HostPort int
	Address  string
	Port     int
}

// LiveContainer is the inspect data host-gateway programming needs.
type LiveContainer struct {
	Name      string
	Service   string
	Running   bool
	Primary   string
	Endpoints []LiveEndpoint
	Ports     []LivePort
}

// LiveEndpoint is one attached network.
type LiveEndpoint struct {
	Network string
	IP      string
	Gateway string
}

// LivePort is one published binding reported by inspect.
type LivePort struct {
	Target   int
	Proto    string
	HostPort int
	HostIP   string
}

// GatewayService is the compose side of route collection.
type GatewayService struct {
	Name     string
	Networks []string // runtime network names
	Ports    []types.ServicePortConfig
	Gateway  bool
}

// CollectRoutes builds the DNAT table for running containers. An empty
// result with a nil error means nothing was published yet.
func CollectRoutes(services []GatewayService, live []LiveContainer) ([]Route, error) {
	byName := map[string]GatewayService{}
	nets := map[string]bool{}
	for _, s := range services {
		byName[s.Name] = s
		if s.Gateway {
			for _, n := range s.Networks {
				nets[n] = true
			}
		}
	}
	var gateways []string
	seenGW := map[string]bool{}
	for _, c := range live {
		if !c.Running {
			continue
		}
		for _, ep := range c.Endpoints {
			if !nets[ep.Network] || ep.Gateway == "" || seenGW[ep.Gateway] {
				continue
			}
			ip, err := netip.ParseAddr(ep.Gateway)
			if err != nil || !ip.Is4() {
				return nil, fmt.Errorf("host-gateway: gateway %q is not IPv4", ep.Gateway)
			}
			seenGW[ep.Gateway] = true
			gateways = append(gateways, ip.String())
		}
	}
	slices.Sort(gateways)
	routes := map[string]Route{}
	for _, c := range live {
		if !c.Running {
			continue
		}
		svc, ok := byName[c.Service]
		if !ok {
			continue
		}
		addr := endpointIP(c)
		if addr == "" {
			continue
		}
		for _, port := range svc.Ports {
			proto := port.Protocol
			if proto == "" {
				proto = "tcp"
			}
			proto = strings.ToLower(proto)
			if proto != "tcp" && proto != "udp" {
				return nil, fmt.Errorf("host-gateway: %s publishes %s, only tcp and udp are supported", svc.Name, proto)
			}
			if port.HostIP != "" && port.HostIP != "0.0.0.0" {
				continue
			}
			published, ranged := atoi(port.Published)
			if strings.Contains(port.Published, "-") {
				continue // a host range is resolved per container and is not a stable DNAT target
			}
			for _, binding := range c.Ports {
				if binding.Target != int(port.Target) || binding.Proto != proto {
					continue
				}
				if ranged && binding.HostPort != published {
					continue
				}
				for _, gw := range gateways {
					key := fmt.Sprintf("%s %s %d", gw, proto, binding.HostPort)
					next := Route{Gateway: gw, Proto: proto, HostPort: binding.HostPort, Address: addr, Port: int(port.Target)}
					if prev, ok := routes[key]; ok && prev != next {
						return nil, fmt.Errorf("host-gateway: %s/%d on %s is published by more than one container", proto, binding.HostPort, gw)
					}
					routes[key] = next
				}
			}
		}
	}
	out := make([]Route, 0, len(routes))
	for _, r := range routes {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Route) int {
		if a.Gateway != b.Gateway {
			return strings.Compare(a.Gateway, b.Gateway)
		}
		if a.Proto != b.Proto {
			return strings.Compare(a.Proto, b.Proto)
		}
		if a.HostPort != b.HostPort {
			return a.HostPort - b.HostPort
		}
		return strings.Compare(a.Address, b.Address)
	})
	return out, nil
}

func endpointIP(c LiveContainer) string {
	for _, ep := range c.Endpoints {
		if ep.Network == c.Primary && ep.IP != "" {
			return ep.IP
		}
	}
	for _, ep := range c.Endpoints {
		if ep.IP != "" {
			return ep.IP
		}
	}
	return ""
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// GatewayStatusPath is the file inside the session VM that records the exit
// status of this project's host-gateway script (/run/<owner>.status).
func GatewayStatusPath(project string) string {
	owner, _ := gatewayIdentity(project)
	return "/run/" + owner + ".status"
}

func gatewayIdentity(project string) (owner, digest string) {
	sum := sha256.Sum256([]byte(project))
	digest = hex.EncodeToString(sum[:])
	owner = "wslc-compose-host-gateway-" + digest
	return owner, digest
}

// GatewayScript is the shell program run by `wslc system session run`.
// cleanup removes the project's chains; otherwise it installs routes.
// Both forms are safe to run again: they hold an exclusive lock, cleanup
// deletes a chain only when it exists, and install replaces rules in place
// (`iptables -C || iptables -I`, then a flush via iptables-restore). The
// client wraps the program with an EXIT trap that records the exit status;
// that wrapper does not change these iptables steps.
func GatewayScript(project string, routes []Route, cleanup bool) (string, error) {
	owner, digest := gatewayIdentity(project)
	inChain := "WCGP_" + digest[:12]
	outChain := "WCGS_" + digest[:12]
	fwdChain := "WCGF_" + digest[:12]
	var b strings.Builder
	b.WriteString("set -eu\n")
	b.WriteString("command -v iptables >/dev/null\n")
	b.WriteString("command -v iptables-restore >/dev/null\n")
	fmt.Fprintf(&b, "exec 9>/run/%s.lock\n", owner)
	b.WriteString("flock -w 10 9\n")
	for _, spec := range []struct{ chain, table, parent string }{
		{inChain, "nat", "PREROUTING"},
		{outChain, "nat", "POSTROUTING"},
		{fwdChain, "filter", "FORWARD"},
	} {
		fmt.Fprintf(&b, "if iptables -w 10 -t %s -S %s >/dev/null 2>&1; then\n", spec.table, spec.chain)
		fmt.Fprintf(&b, "  iptables -w 10 -t %s -S %s | grep -F -- %s >/dev/null || exit 71\n", spec.table, spec.chain, owner)
		if cleanup {
			fmt.Fprintf(&b, "  while iptables -w 10 -t %s -C %s -j %s 2>/dev/null; do\n", spec.table, spec.parent, spec.chain)
			fmt.Fprintf(&b, "    iptables -w 10 -t %s -D %s -j %s\n", spec.table, spec.parent, spec.chain)
			b.WriteString("  done\n")
			fmt.Fprintf(&b, "  iptables -w 10 -t %s -F %s\n", spec.table, spec.chain)
			fmt.Fprintf(&b, "  iptables -w 10 -t %s -X %s\n", spec.table, spec.chain)
			b.WriteString("fi\n")
			continue
		}
		b.WriteString("else\n")
		fmt.Fprintf(&b, "  iptables -w 10 -t %s -N %s\n", spec.table, spec.chain)
		fmt.Fprintf(&b, "  iptables -w 10 -t %s -A %s -m comment --comment %s -j RETURN\n", spec.table, spec.chain, owner)
		b.WriteString("fi\n")
	}
	if cleanup {
		return b.String(), nil
	}
	for _, r := range routes {
		if r.Proto != "tcp" && r.Proto != "udp" {
			return "", fmt.Errorf("host-gateway: bad protocol %q", r.Proto)
		}
		if r.HostPort < 1 || r.HostPort > 65535 || r.Port < 1 || r.Port > 65535 {
			return "", fmt.Errorf("host-gateway: port out of range")
		}
		gw, err1 := netip.ParseAddr(r.Gateway)
		dst, err2 := netip.ParseAddr(r.Address)
		if err1 != nil || err2 != nil || !gw.Is4() || !dst.Is4() {
			return "", fmt.Errorf("host-gateway: route addresses must be IPv4")
		}
	}
	b.WriteString("iptables-restore --wait 10 --noflush <<'WCG_RULES'\n")
	fmt.Fprintf(&b, "*nat\n-F %s\n-F %s\n", inChain, outChain)
	for _, r := range routes {
		fmt.Fprintf(&b, "-A %s -d %s/32 -p %s --dport %d -j DNAT --to-destination %s:%d\n", inChain, r.Gateway, r.Proto, r.HostPort, r.Address, r.Port)
		fmt.Fprintf(&b, "-A %s -d %s/32 -p %s --dport %d -m conntrack --ctorigdst %s --ctorigdstport %d -j MASQUERADE\n", outChain, r.Address, r.Proto, r.Port, r.Gateway, r.HostPort)
	}
	fmt.Fprintf(&b, "-A %s -m comment --comment %s -j RETURN\n", inChain, owner)
	fmt.Fprintf(&b, "-A %s -m comment --comment %s -j RETURN\nCOMMIT\n", outChain, owner)
	fmt.Fprintf(&b, "*filter\n-F %s\n", fwdChain)
	for _, r := range routes {
		fmt.Fprintf(&b, "-A %s -d %s/32 -p %s --dport %d -m conntrack --ctdir ORIGINAL --ctorigdst %s --ctorigdstport %d -j ACCEPT\n", fwdChain, r.Address, r.Proto, r.Port, r.Gateway, r.HostPort)
		fmt.Fprintf(&b, "-A %s -s %s/32 -p %s --sport %d -m conntrack --ctstate ESTABLISHED --ctdir REPLY --ctorigdst %s --ctorigdstport %d -j ACCEPT\n", fwdChain, r.Address, r.Proto, r.Port, r.Gateway, r.HostPort)
	}
	fmt.Fprintf(&b, "-A %s -m comment --comment %s -j RETURN\nCOMMIT\nWCG_RULES\n", fwdChain, owner)
	fmt.Fprintf(&b, "iptables -w 10 -t nat -C PREROUTING -j %s 2>/dev/null || iptables -w 10 -t nat -I PREROUTING 1 -j %s\n", inChain, inChain)
	fmt.Fprintf(&b, "iptables -w 10 -t nat -C POSTROUTING -j %s 2>/dev/null || iptables -w 10 -t nat -I POSTROUTING 1 -j %s\n", outChain, outChain)
	fmt.Fprintf(&b, "if ! iptables -w 10 -t filter -C FORWARD -j %s 2>/dev/null; then\n", fwdChain)
	b.WriteString("  position=$(iptables -w 10 -t filter -S FORWARD | awk '$1 == \"-A\" {i++; if ($0 == \"-A FORWARD -j DOCKER-USER\") {print i+1; exit}}')\n")
	b.WriteString("  [ -n \"$position\" ] || exit 72\n")
	fmt.Fprintf(&b, "  iptables -w 10 -t filter -I FORWARD \"$position\" -j %s\nfi\n", fwdChain)
	return b.String(), nil
}
