package paths

import (
	"errors"
	"testing"
)

func TestFromMnt(t *testing.T) {
	for in, want := range map[string]string{
		"/mnt/c/work/app": "C:/work/app",
		"/mnt/d":          "D:/",
		"/home/u/x":       "",
		"/mnt/cc/x":       "",
	} {
		got, ok := FromMnt(in)
		if ok != (want != "") || got != want {
			t.Errorf("FromMnt(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}
}

func TestWSLMapper(t *testing.T) {
	calls := 0
	m := WSL(func(p string) (string, error) {
		calls++
		if p == "/broken" {
			return "", errors.New("boom")
		}
		return `\\wsl.localhost\Ubuntu` + p, nil
	})
	cases := map[string]string{
		`C:\src\app`:   "C:/src/app",
		"/mnt/e/data":  "E:/data",
		"/home/u/proj": "//wsl.localhost/Ubuntu/home/u/proj",
		"/broken":      "/broken",
	}
	for in, want := range cases {
		if got := m(in); got != want {
			t.Errorf("map(%q) = %q; want %q", in, got, want)
		}
	}
	m("/home/u/proj") // cached
	if calls != 2 {
		t.Errorf("expected 2 converter calls, got %d", calls)
	}
}

func TestSlashAndIsWindows(t *testing.T) {
	if Slash(`C:\a\b`) != "C:/a/b" {
		t.Fatal("Slash failed")
	}
	if !IsWindows("c:/x") || !IsWindows(`\\srv\share`) || IsWindows("/x") {
		t.Fatal("IsWindows misclassified")
	}
	if Identity("x") != "x" {
		t.Fatal("Identity")
	}
}
