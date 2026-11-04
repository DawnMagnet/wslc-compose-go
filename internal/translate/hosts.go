package translate

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// UsesHostGateway reports whether any extra_hosts entry asks for the
// Compose host-gateway token.
func UsesHostGateway(h types.HostsList) bool {
	for _, addrs := range h {
		for _, addr := range addrs {
			if strings.EqualFold(strings.TrimSpace(addr), "host-gateway") {
				return true
			}
		}
	}
	return false
}

// ExtraHostsScript is a POSIX shell script that rewrites the managed
// /etc/hosts lines of one container. host-gateway is resolved from the
// container's default route. The awk that decodes /proc/net/route follows
// the approach in bacarndiaye/wslc-compose (MIT); the rest of the script is
// ours, including the marker that makes a later start idempotent.
//
// wslc has no --add-host, so the script runs via `wslc exec` after start.
// The entrypoint may already have read /etc/hosts.
func ExtraHostsScript(hosts types.HostsList) (string, error) {
	type row struct{ host, addr string }
	var rows []row
	gateway := false
	for _, host := range slices.Sorted(maps.Keys(hosts)) {
		if !validHost(host) {
			return "", fmt.Errorf("extra_hosts: invalid hostname %q", host)
		}
		addrs := append([]string(nil), hosts[host]...)
		slices.Sort(addrs)
		for _, addr := range addrs {
			addr = strings.TrimSpace(addr)
			if strings.EqualFold(addr, "host-gateway") {
				gateway = true
				rows = append(rows, row{host, "$GW"})
				continue
			}
			ip, err := netip.ParseAddr(addr)
			if err != nil {
				return "", fmt.Errorf("extra_hosts: %s has invalid address %q", host, addr)
			}
			rows = append(rows, row{host, ip.String()})
		}
	}
	if len(rows) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("set -eu\n")
	if gateway {
		// Little-endian hex default route (destination 00000000) from /proc/net/route.
		b.WriteString("GW=$(awk '$2 == \"00000000\" { h=$3; for (i=7; i>=1; i-=2) { hi=index(\"0123456789ABCDEF\", substr(h,i,1))-1; lo=index(\"0123456789ABCDEF\", substr(h,i+1,1))-1; printf \"%s%d\", (i==7 ? \"\" : \".\"), hi*16+lo } print \"\"; exit }' /proc/net/route 2>/dev/null || true)\n")
		b.WriteString("if [ -z \"$GW\" ]; then echo 'wslc-compose: host-gateway address not found' >&2; exit 1; fi\n")
	}
	b.WriteString("tmp=$(mktemp)\n")
	b.WriteString("grep -v 'wslc-compose-extra-host' /etc/hosts > \"$tmp\" || true\n")
	b.WriteString("cat \"$tmp\" > /etc/hosts\n")
	b.WriteString("rm -f \"$tmp\"\n")
	for _, row := range rows {
		if row.addr == "$GW" {
			fmt.Fprintf(&b, "printf '%%s %s # wslc-compose-extra-host\\n' \"$GW\" >> /etc/hosts\n", row.host)
			continue
		}
		fmt.Fprintf(&b, "printf '%%s %s # wslc-compose-extra-host\\n' '%s' >> /etc/hosts\n", row.host, row.addr)
	}
	return b.String(), nil
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " \t\n'\"\\$`|&;<>") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}
