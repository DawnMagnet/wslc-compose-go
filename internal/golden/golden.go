// Package golden compares test output with files under testdata/golden.
// Run `go test ./... -update` to rewrite them after an intended change.
package golden

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Root is the repository root (resolved from this source file).
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// Compose returns the absolute path of a file in testdata/compose.
func Compose(name string) string { return filepath.Join(Root(), "testdata", "compose", name) }

// Assert compares got with testdata/golden/<name>, rewriting it with -update.
func Assert(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(Root(), "testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run go test ./... -update): %v", name, err)
	}
	if string(want) != got {
		t.Errorf("output differs from %s (run go test ./... -update to accept)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// Paths is a paths.Mapper for tests: it rewrites testdata/compose to C:/proj
// so golden files do not depend on the checkout location.
func Paths(p string) string {
	return strings.Replace(filepath.ToSlash(p), filepath.ToSlash(filepath.Join(Root(), "testdata", "compose")), "C:/proj", 1)
}
