//go:build unix

package wslc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSessionTrapKeepsExitWhenStatusWriteFails checks that a failing mv,
// including a read-only status directory, does not replace the script's exit
// code and does not print the write error. set -e stays on, which is what
// makes a bare `&& mv` abort the EXIT trap.
func TestSessionTrapKeepsExitWhenStatusWriteFails(t *testing.T) {
	shells := statusShells(t)
	for _, sh := range shells {
		for _, code := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/mv-fails/%d", sh.name, code), func(t *testing.T) {
				dir := t.TempDir()
				// A dropped-privilege shell must be able to create the temp
				// file so that mv, not the directory mode, is what fails.
				if err := os.Chmod(dir, 0o777); err != nil {
					t.Fatal(err)
				}
				bin := filepath.Join(dir, "bin")
				if err := os.Mkdir(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bin, "mv"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				status := filepath.ToSlash(filepath.Join(dir, "s.status"))
				got, stderr := runStatusScript(t, sh, fmt.Sprintf("set -eu\nexit %d\n", code), status, bin+":"+os.Getenv("PATH"))
				if got != code {
					t.Fatalf("exit %d, want %d; stderr %q", got, code, stderr)
				}
				if stderr != "" {
					t.Fatalf("stderr %q", stderr)
				}
				assertNoStatusFiles(t, dir)
			})
			t.Run(fmt.Sprintf("%s/readonly/%d", sh.name, code), func(t *testing.T) {
				dir := t.TempDir()
				ro := filepath.Join(dir, "ro")
				if err := os.Mkdir(ro, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(ro, 0o555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
				status := filepath.ToSlash(filepath.Join(ro, "s.status"))
				got, stderr := runStatusScript(t, sh, fmt.Sprintf("set -eu\nexit %d\n", code), status, "")
				if got != code {
					t.Fatalf("exit %d, want %d; stderr %q", got, code, stderr)
				}
				if stderr != "" {
					t.Fatalf("stderr %q", stderr)
				}
				assertNoStatusFiles(t, ro)
			})
		}
	}
}

type statusShell struct {
	name string
	bin  string
	args []string
	// cred drops privileges so a read-only directory rejects the write when
	// the test process is root.
	cred *syscall.Credential
}

func statusShells(t *testing.T) []statusShell {
	t.Helper()
	var out []statusShell
	if p, err := exec.LookPath("bash"); err == nil {
		out = append(out, statusShell{name: "bash", bin: p})
		// bash entered as argv0=sh runs in POSIX mode.
		link := filepath.Join(t.TempDir(), "sh")
		if err := os.Symlink(p, link); err == nil {
			out = append(out, statusShell{name: "bash-as-sh", bin: link})
		}
	}
	if p, err := exec.LookPath("dash"); err == nil {
		out = append(out, statusShell{name: "dash", bin: p})
	}
	if len(out) == 0 {
		t.Skip("no bash or dash")
	}
	cred := unprivCred(t)
	for i := range out {
		out[i].cred = cred
	}
	return out
}

// unprivCred is a uid/gid that does not bypass directory permissions. Nil
// when the current user already cannot.
func unprivCred(t *testing.T) *syscall.Credential {
	t.Helper()
	if os.Geteuid() != 0 {
		return nil
	}
	u, err := user.Lookup("nobody")
	if err != nil {
		t.Fatalf("root cannot use a read-only directory without nobody: %v", err)
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
}

func runStatusScript(t *testing.T, sh statusShell, body, status, path string) (int, string) {
	t.Helper()
	script := wrapSessionScript(body, status, "abc1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	argv := append(append([]string{}, sh.args...), "-c", script)
	cmd := exec.CommandContext(ctx, sh.bin, argv...)
	cmd.Dir = filepath.Dir(filepath.FromSlash(status))
	if path != "" {
		env := make([]string, 0, len(os.Environ()))
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "PATH=") {
				env = append(env, e)
			}
		}
		cmd.Env = append(env, "PATH="+path)
	}
	if sh.cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: sh.cred}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run: %v\nstderr %s", err, stderr.String())
		}
		code = ee.ExitCode()
	}
	return code, stderr.String()
}

func assertNoStatusFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "status") {
			t.Fatalf("status file left behind: %s", e.Name())
		}
	}
}
