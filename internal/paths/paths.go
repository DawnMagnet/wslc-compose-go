// Package paths converts host paths into the form wslc.exe accepts.
//
// wslc runs on the Windows side and shares bind mounts over VirtioFS. It
// expects Windows paths written with forward slashes (C:/work), because a
// backslash is treated as an escape character. When wslc-compose itself runs
// inside a WSL distro and drives wslc.exe, Linux paths are first translated
// with `wslpath -w`.
package paths

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// Mapper turns a local absolute path into the path string passed to wslc.
type Mapper func(string) string

// Identity is the Mapper that returns paths unchanged.
func Identity(p string) string { return p }

var (
	winAbs = regexp.MustCompile(`^[A-Za-z]:[\\/]|^\\\\|^//`)
	mntDrv = regexp.MustCompile(`^/mnt/([A-Za-z])(/.*)?$`)
)

// IsWindows reports whether p already is a Windows path (drive or UNC).
func IsWindows(p string) bool { return winAbs.MatchString(p) }

// Slash converts backslashes to forward slashes (C:\a\b -> C:/a/b).
func Slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// FromMnt maps a WSL automount path (/mnt/c/x) to its drive form (C:/x)
// without spawning wslpath. ok is false for any other path.
func FromMnt(p string) (string, bool) {
	m := mntDrv.FindStringSubmatch(p)
	if m == nil {
		return "", false
	}
	rest := m[2]
	if rest == "" {
		rest = "/"
	}
	return strings.ToUpper(m[1]) + ":" + rest, true
}

// InWSL reports whether the current process runs inside a WSL distro.
func InWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// For returns the Mapper appropriate for driving the wslc binary at bin:
//   - native Windows: forward slashes only;
//   - inside WSL calling a *.exe: wslpath -w, then forward slashes;
//   - anything else (plain Linux, tests): identity.
func For(bin string) Mapper {
	switch {
	case runtime.GOOS == "windows":
		return Slash
	case InWSL() && strings.HasSuffix(strings.ToLower(bin), ".exe"):
		return WSL(wslpath)
	default:
		return Identity
	}
}

// WSL builds a caching Mapper for WSL -> Windows translation. conv is the
// fallback converter for paths outside /mnt/<drive> (normally `wslpath -w`);
// when it fails the path is passed through unchanged.
func WSL(conv func(string) (string, error)) Mapper {
	var mu sync.Mutex
	cache := map[string]string{}
	return func(p string) string {
		if p == "" || IsWindows(p) {
			return Slash(p)
		}
		if w, ok := FromMnt(p); ok {
			return w
		}
		mu.Lock()
		defer mu.Unlock()
		if w, ok := cache[p]; ok {
			return w
		}
		w := p
		if out, err := conv(p); err == nil && out != "" {
			w = Slash(out)
		}
		cache[p] = w
		return w
	}
}

func wslpath(p string) (string, error) {
	out, err := exec.Command("wslpath", "-w", p).Output()
	return strings.TrimSpace(string(out)), err
}
