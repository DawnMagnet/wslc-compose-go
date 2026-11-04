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

// wslcVersionRE matches wslc's own version, not the first dotted number in
// the output (WSL's version and the Go toolchain version often appear too).
// A pre-release suffix is captured separately and compares below the release.
var wslcVersionRE = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])wslc(?:\.exe)?(?:[ \t]+version)?[ \t]+v?(\d+(?:\.\d+){1,3})(?:-([0-9A-Za-z.]+))?`)

// Capabilities is what a probed wslc binary can be asked to do.
type Capabilities struct {
	Raw   string // version text, empty when probing failed
	Known bool   // Raw contained a parseable wslc version
	parts []int
	pre   string // pre-release identifier; empty for a release

	// Assumed is set when no binary could be probed and the caller opted
	// into planning as if a current wslc were present (dry-run).
	Assumed bool
}

// ParseCapabilities extracts the wslc version from `wslc --version` output.
// A bare semver with no wslc token is ignored. A pre-release such as
// 3.0.3.0-rc1 is known but compares below 3.0.3.0.
func ParseCapabilities(raw string) Capabilities {
	m := wslcVersionRE.FindStringSubmatch(raw)
	if m == nil {
		return Capabilities{Raw: strings.TrimSpace(raw)}
	}
	var parts []int
	for _, f := range strings.Split(m[1], ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			return Capabilities{Raw: strings.TrimSpace(raw)}
		}
		parts = append(parts, n)
	}
	return Capabilities{Raw: strings.TrimSpace(raw), Known: true, parts: parts, pre: m[2]}
}

// AssumeCurrent plans every gated feature. Used when --dry-run cannot see a
// wslc binary and the preview should still show the full command sequence.
func AssumeCurrent() Capabilities {
	c := ParseCapabilities("wslc version " + VerHostGateway)
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
	want := ParseCapabilities("wslc version " + dotted)
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
	// Equal numbers: a pre-release is older than the release itself.
	return cmpPre(c.pre, want.pre) >= 0
}

// cmpPre compares pre-release identifiers. An empty identifier is a release
// and is greater than any pre-release. Numeric chunks compare as integers,
// so rc10 is newer than rc2.
func cmpPre(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := splitPre(a), splitPre(b)
	n := max(len(as), len(bs))
	for i := 0; i < n; i++ {
		if i >= len(as) {
			return -1
		}
		if i >= len(bs) {
			return 1
		}
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil && an != bn:
			return an - bn
		case aerr == nil && berr == nil:
			continue
		case aerr == nil && berr != nil:
			return -1 // numeric identifiers have lower precedence
		case aerr != nil && berr == nil:
			return 1
		}
		if as[i] != bs[i] {
			return strings.Compare(as[i], bs[i])
		}
	}
	return 0
}

func splitPre(s string) []string {
	var out []string
	var cur strings.Builder
	var kind int // 0 unset, 1 digit, 2 other
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		next := 2
		if r == '.' {
			flush()
			kind = 0
			continue
		}
		if r >= '0' && r <= '9' {
			next = 1
		}
		if kind != 0 && next != kind {
			flush()
		}
		kind = next
		cur.WriteRune(r)
	}
	flush()
	return out
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
