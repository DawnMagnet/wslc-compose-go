package wslc

import (
	"regexp"
	"strconv"
	"strings"
)

// Feature floors. Each constant is the first wslc release known to accept
// the feature, taken from the WSL source history and from notes shipped with
// bacarndiaye/wslc-compose. None of these floors has been re-checked against
// a live wslc binary in this repository; an older binary degrades instead of
// sending a flag it will reject.
const (
	// VerEndpoints is `network connect` (with --ip, --network-alias,
	// --link-local-ip and --driver-opt), `--ip` on run/create, and
	// `network create --ip-range`.
	VerEndpoints = "3.0.3.0"
	// VerHostGateway is `wslc system session run` plus iptables inside the
	// session VM, which host-gateway published-port loopback needs.
	VerHostGateway = "3.0.3.0"
	// VerBuildSecret is `wslc build --secret id=...,src=...`.
	VerBuildSecret = "3.0.3.0"
)

var versionRE = regexp.MustCompile(`\d+(?:\.\d+){1,3}`)

// Capabilities is what a probed wslc binary can be asked to do.
type Capabilities struct {
	Raw   string // version text, empty when probing failed
	Known bool   // Raw contained a parseable version
	parts []int

	// Assumed is set when no binary could be probed and the caller opted
	// into planning as if a current wslc were present (dry-run).
	Assumed bool
}

// ParseCapabilities extracts a dotted version from `wslc --version` output.
func ParseCapabilities(raw string) Capabilities {
	m := versionRE.FindString(raw)
	if m == "" {
		return Capabilities{Raw: strings.TrimSpace(raw)}
	}
	var parts []int
	for _, f := range strings.Split(m, ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			return Capabilities{Raw: strings.TrimSpace(raw)}
		}
		parts = append(parts, n)
	}
	return Capabilities{Raw: strings.TrimSpace(raw), Known: true, parts: parts}
}

// AssumeCurrent plans every gated feature. Used when --dry-run cannot see a
// wslc binary and the preview should still show the full command sequence.
func AssumeCurrent() Capabilities {
	c := ParseCapabilities(VerHostGateway)
	c.Raw = ""
	c.Assumed = true
	return c
}

// AtLeast reports whether the probed version is at least dotted (for example
// "3.0.3.0"). An unknown version is never new enough; an assumed one is.
func (c Capabilities) AtLeast(dotted string) bool {
	if c.Assumed {
		return true
	}
	if !c.Known {
		return false
	}
	want := ParseCapabilities(dotted)
	if !want.Known {
		return false
	}
	n := max(len(c.parts), len(want.parts))
	for i := 0; i < n; i++ {
		a, b := 0, 0
		if i < len(c.parts) {
			a = c.parts[i]
		}
		if i < len(want.parts) {
			b = want.parts[i]
		}
		if a != b {
			return a > b
		}
	}
	return true
}

// String is the version text, or "unknown".
func (c Capabilities) String() string {
	if c.Assumed {
		return "unknown (dry-run assumes " + VerHostGateway + ")"
	}
	if c.Raw == "" {
		return "unknown"
	}
	return c.Raw
}
