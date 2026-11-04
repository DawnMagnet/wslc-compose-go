package translate

import (
	"fmt"
	"maps"
	"net"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
)

// Create is the argv for `wslc create`: the same container spec as Run, but
// the container is left stopped so additional networks can be connected
// before the entrypoint runs. wslc run accepts a single --network, so
// multi-network services use create → network connect → start.
func (t *Translator) Create(s types.ServiceConfig, o RunOptions) []string {
	o.Detach = false
	args := t.Run(s, o)
	if len(args) > 0 {
		args[0] = "create"
	}
	return args
}

// Connect is the argv for `wslc network connect` of one secondary endpoint.
// Aliases include the service name, matching the primary --network-alias set.
func (t *Translator) Connect(s types.ServiceConfig, key, container string) []string {
	a := argv{"network", "connect"}
	cfg := s.Networks[key]
	if cfg != nil && !t.OmitStaticIP && cfg.Ipv4Address != "" {
		a.add("--ip", cfg.Ipv4Address)
	}
	if cfg != nil {
		for _, ip := range cfg.LinkLocalIPs {
			a.add("--link-local-ip", ip)
		}
		for _, k := range slices.Sorted(maps.Keys(cfg.DriverOpts)) {
			a.add("--driver-opt", k+"="+cfg.DriverOpts[k])
		}
	}
	a.each("--network-alias", endpointAliases(s, key))
	a.add(t.networkName(key), container)
	return a
}

func (t *Translator) networkName(key string) string {
	if t.Project != nil {
		if n, ok := t.Project.Networks[key]; ok && n.Name != "" {
			return n.Name
		}
	}
	return key
}

func endpointAliases(s types.ServiceConfig, key string) []string {
	names := []string{s.Name}
	if cfg := s.Networks[key]; cfg != nil {
		names = append(names, cfg.Aliases...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// ValidateServiceNetworks checks static addresses and IPAM.
// Address checks run only when staticIP is set, because older wslc builds
// never receive --ip and v0.1.5 only warned. staticIP is the 3.0.3 endpoint
// feature set, which is also when --ip-range is sent. A pool is a hard error
// when this process will create that network and a service puts a static
// IPv4 address on it, or when --ip-range would be sent for a range outside
// the subnet, or when the range has no usable IPv4 subnet at all. IPv6-only
// subnets (no ip_range) and prefixes tighter than /30 stay warnings and the
// subnet is still passed to wslc. External networks are not validated.
func ValidateServiceNetworks(p *types.Project, staticIP bool) (warnings []string, err error) {
	if staticIP {
		if err = validateAddresses(p); err != nil {
			return nil, err
		}
	}
	staticOn := map[string]bool{}
	if staticIP {
		for _, name := range p.ServiceNames() {
			s := p.Services[name]
			if s.NetworkMode != "" {
				continue
			}
			for key, cfg := range s.Networks {
				if cfg != nil && cfg.Ipv4Address != "" {
					staticOn[key] = true
				}
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(p.Networks)) {
		n := p.Networks[key]
		if bool(n.External) || !networkUsed(p, key) {
			continue
		}
		msg := poolProblem(key, n)
		if msg == "" {
			continue
		}
		// --ip-range is new in 3.0.3. A range that is not inside an IPv4
		// subnet must fail before network create, even with no static address.
		if staticOn[key] || (staticIP && rangeOutside(n)) {
			return nil, fmt.Errorf("%s", msg)
		}
		warnings = append(warnings, msg)
	}
	return warnings, nil
}

func networkUsed(p *types.Project, key string) bool {
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		if s.NetworkMode != "" {
			continue
		}
		if len(s.Networks) == 0 {
			if key == "default" {
				return true
			}
			continue
		}
		if _, ok := s.Networks[key]; ok {
			return true
		}
	}
	return false
}

func validateAddresses(p *types.Project) error {
	seen := map[string]string{}
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		if s.NetworkMode != "" {
			continue
		}
		for _, key := range s.NetworksByPriority() {
			cfg := s.Networks[key]
			if cfg == nil || cfg.Ipv4Address == "" {
				continue
			}
			if s.GetScale() > 1 {
				return fmt.Errorf("service %q: static ipv4_address cannot be shared by %d replicas", name, s.GetScale())
			}
			ip := net.ParseIP(cfg.Ipv4Address).To4()
			if ip == nil {
				return fmt.Errorf("service %q network %q: invalid ipv4_address %q", name, key, cfg.Ipv4Address)
			}
			id := key + " " + ip.String()
			if other, ok := seen[id]; ok {
				return fmt.Errorf("service %q network %q: ipv4_address %s is also requested by %s", name, key, ip, other)
			}
			seen[id] = name
			if n := p.Networks[key]; bool(n.External) {
				continue
			}
			if err := addressInNetwork(name, key, ip, p.Networks[key]); err != nil {
				return err
			}
		}
	}
	return nil
}

func addressInNetwork(service, key string, ip net.IP, n types.NetworkConfig) error {
	pool, prefix := firstPool(n)
	if prefix == nil {
		return nil
	}
	if !usable4(prefix, ip) {
		return fmt.Errorf("service %q network %q: ipv4_address %s is not a usable address in %s", service, key, ip, prefix)
	}
	if pool.Gateway != "" && ip.Equal(net.ParseIP(pool.Gateway).To4()) {
		return fmt.Errorf("service %q network %q: ipv4_address %s is the gateway", service, key, ip)
	}
	return nil
}

// rangeOutside reports an ip_range that 3.0.3 would pass to --ip-range
// and that is not an IPv4 range inside an IPv4 subnet. A missing subnet or
// a non-IPv4 subnet has no usable IPv4 prefix, so the range is invalid.
func rangeOutside(n types.NetworkConfig) bool {
	pool, prefix := firstPool(n)
	if pool == nil || pool.IPRange == "" {
		return false
	}
	if prefix == nil {
		return true
	}
	_, rng, err := net.ParseCIDR(pool.IPRange)
	return err != nil || rng.IP.To4() == nil || !cidrInside(prefix, rng)
}

func poolProblem(key string, n types.NetworkConfig) string {
	pool, prefix := firstPool(n)
	if pool == nil {
		return ""
	}
	if pool.Subnet == "" && (pool.Gateway != "" || pool.IPRange != "") {
		return fmt.Sprintf("network %q: ipam gateway/ip_range requires a subnet", key)
	}
	if pool.Subnet == "" {
		return ""
	}
	if prefix == nil {
		if pool.IPRange != "" {
			return fmt.Sprintf("network %q: ip_range %q requires an IPv4 subnet", key, pool.IPRange)
		}
		return fmt.Sprintf("network %q: invalid IPv4 subnet %q", key, pool.Subnet)
	}
	ones, _ := prefix.Mask.Size()
	if ones > 30 {
		return fmt.Sprintf("network %q: subnet %s needs network, gateway and broadcast addresses", key, prefix)
	}
	if pool.Gateway != "" {
		gw := net.ParseIP(pool.Gateway).To4()
		if gw == nil || !usable4(prefix, gw) {
			return fmt.Sprintf("network %q: gateway %q is not a usable address in %s", key, pool.Gateway, prefix)
		}
	}
	if pool.IPRange != "" {
		_, rng, err := net.ParseCIDR(pool.IPRange)
		if err != nil || rng.IP.To4() == nil || !cidrInside(prefix, rng) {
			return fmt.Sprintf("network %q: ip_range %q is not an IPv4 range inside %s", key, pool.IPRange, prefix)
		}
	}
	return ""
}

func firstPool(n types.NetworkConfig) (*types.IPAMPool, *net.IPNet) {
	if len(n.Ipam.Config) == 0 || n.Ipam.Config[0] == nil || n.Ipam.Config[0].Subnet == "" {
		if len(n.Ipam.Config) == 0 || n.Ipam.Config[0] == nil {
			return nil, nil
		}
		return n.Ipam.Config[0], nil
	}
	_, prefix, err := net.ParseCIDR(n.Ipam.Config[0].Subnet)
	if err != nil || prefix.IP.To4() == nil {
		return n.Ipam.Config[0], nil
	}
	return n.Ipam.Config[0], prefix
}

func usable4(n *net.IPNet, ip net.IP) bool {
	ip = ip.To4()
	if ip == nil || !n.Contains(ip) {
		return false
	}
	base := n.IP.To4()
	if ip.Equal(base) {
		return false
	}
	bcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		bcast[i] = base[i] | ^n.Mask[i]
	}
	return !ip.Equal(bcast)
}

func cidrInside(outer, inner *net.IPNet) bool {
	if !outer.Contains(inner.IP) {
		return false
	}
	ip := inner.IP.To4()
	if ip == nil {
		return false
	}
	last := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		last[i] = ip[i] | ^inner.Mask[i]
	}
	return outer.Contains(last)
}
