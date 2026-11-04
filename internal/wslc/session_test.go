package wslc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const statusPath = "/run/wslc-compose-host-gateway-abc.status"

func TestSessionInvalidHandleUsesRecordedStatus(t *testing.T) {
	t.Run("status 0", func(t *testing.T) {
		scripts, cats := 0, 0
		recorded := ""
		c := newSessionClient(t, 5, func(args []string, stdio IO) error {
			requireSessionIO(t, stdio)
			script := args[len(args)-1]
			if strings.HasPrefix(script, "cat ") {
				cats++
				if script != "cat "+statusPath {
					t.Fatalf("status read: %q", script)
				}
				io.WriteString(stdio.Stdout, recorded)
				// wslc often prints the output and then reports a bad handle.
				return &Error{Args: args, Code: 1, Stderr: "句柄无效。 错误代码： ERROR_INVALID_HANDLE"}
			}
			scripts++
			if !strings.Contains(script, "trap ") || !strings.Contains(script, "echo done") {
				t.Fatalf("script was not wrapped:\n%s", script)
			}
			recorded = sessionField(script, "_wcg_nonce") + " 0\n"
			return &Error{Args: args, Code: 1, Stderr: "句柄无效。 错误代码： ERROR_INVALID_HANDLE"}
		})
		if err := c.Session(context.Background(), "echo done\n", statusPath); err != nil {
			t.Fatal(err)
		}
		if scripts != 1 || cats != 1 {
			t.Fatalf("script ran %d times, cat %d times; status 0 must not re-run the script", scripts, cats)
		}
	})

	t.Run("cat retries then status 0", func(t *testing.T) {
		scripts, cats := 0, 0
		recorded := ""
		c := newSessionClient(t, 5, func(args []string, stdio IO) error {
			requireSessionIO(t, stdio)
			script := args[len(args)-1]
			if strings.HasPrefix(script, "cat ") {
				cats++
				if cats == 1 {
					return &Error{Args: args, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
				}
				io.WriteString(stdio.Stdout, recorded)
				return nil
			}
			scripts++
			recorded = sessionField(script, "_wcg_nonce") + " 0\n"
			return &Error{Args: args, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
		})
		if err := c.Session(context.Background(), "echo done\n", statusPath); err != nil {
			t.Fatal(err)
		}
		if scripts != 1 || cats != 2 {
			t.Fatalf("script %d cat %d; a failed status read must be retried without re-running the script", scripts, cats)
		}
	})

	t.Run("status 72", func(t *testing.T) {
		scripts := 0
		recorded := ""
		c := newSessionClient(t, 5, func(args []string, stdio IO) error {
			requireSessionIO(t, stdio)
			script := args[len(args)-1]
			if strings.HasPrefix(script, "cat ") {
				io.WriteString(stdio.Stdout, recorded)
				return nil
			}
			scripts++
			recorded = sessionField(script, "_wcg_nonce") + " 72\n"
			return &Error{Args: args, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
		})
		err := c.Session(context.Background(), "exit 72\n", statusPath)
		if err == nil || !strings.Contains(err.Error(), "72") || ExitCode(err) != 72 {
			t.Fatalf("status 72: %v", err)
		}
		if scripts != 1 {
			t.Fatalf("a recorded failure must not re-run the script (%d)", scripts)
		}
	})

	t.Run("no status then success", func(t *testing.T) {
		scripts := 0
		c := newSessionClient(t, 5, func(args []string, stdio IO) error {
			requireSessionIO(t, stdio)
			script := args[len(args)-1]
			if strings.HasPrefix(script, "cat ") {
				return &Error{Args: args, Code: 1, Stderr: "cat: No such file or directory"}
			}
			scripts++
			if scripts == 1 {
				return &Error{Args: args, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
			}
			return nil
		})
		if err := c.Session(context.Background(), "echo ok\n", statusPath); err != nil {
			t.Fatal(err)
		}
		if scripts != 2 {
			t.Fatalf("missing status should retry the script once, got %d", scripts)
		}
	})

	t.Run("persistent missing status", func(t *testing.T) {
		const attempts = 4
		scripts := 0
		c := newSessionClient(t, attempts, func(args []string, stdio IO) error {
			requireSessionIO(t, stdio)
			if strings.HasPrefix(args[len(args)-1], "cat ") {
				return &Error{Args: args, Code: 1, Stderr: "cat: No such file or directory"}
			}
			scripts++
			return &Error{Args: args, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
		})
		err := c.Session(context.Background(), "echo ok\n", statusPath)
		if err == nil || !strings.Contains(err.Error(), "no status") || !strings.Contains(err.Error(), "4") {
			t.Fatalf("persistent missing status: %v", err)
		}
		if scripts != attempts {
			t.Fatalf("got %d script runs, want %d", scripts, attempts)
		}
	})

	t.Run("persistent unreadable status", func(t *testing.T) {
		const attempts = 3
		scripts, cats := 0, 0
		c := newSessionClient(t, attempts, func(args []string, stdio IO) error {
			requireSessionIO(t, stdio)
			if strings.HasPrefix(args[len(args)-1], "cat ") {
				cats++
				return &Error{Args: args, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
			}
			scripts++
			return &Error{Args: args, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
		})
		err := c.Session(context.Background(), "echo ok\n", statusPath)
		if err == nil || !strings.Contains(err.Error(), "could not be read") {
			t.Fatalf("unreadable status: %v", err)
		}
		if scripts != 1 || cats != attempts {
			t.Fatalf("script %d cat %d; cat is retried, the script is not", scripts, cats)
		}
	})

	t.Run("script exit propagates", func(t *testing.T) {
		n := 0
		c := newSessionClient(t, 5, func(args []string, stdio IO) error {
			n++
			requireSessionIO(t, stdio)
			return &Error{Args: args, Code: 3, Stderr: "boom"}
		})
		err := c.Session(context.Background(), "exit 3\n", statusPath)
		if ExitCode(err) != 3 || n != 1 {
			t.Fatalf("exit 3 must not be retried: %v after %d", err, n)
		}
	})

	t.Run("locale text without the token", func(t *testing.T) {
		n := 0
		c := newSessionClient(t, 5, func(args []string, _ IO) error {
			n++
			return &Error{Args: args, Code: 1, Stderr: "句柄无效。"}
		})
		err := c.Session(context.Background(), "false\n", statusPath)
		if err == nil || n != 1 {
			t.Fatalf("locale text alone must not retry: %v after %d", err, n)
		}
	})
}

func TestSessionHappyPathAndDryRun(t *testing.T) {
	n := 0
	c := newSessionClient(t, 5, func(args []string, stdio IO) error {
		n++
		requireSessionIO(t, stdio)
		if strings.HasPrefix(args[len(args)-1], "cat ") {
			t.Fatal("successful session must not read the status file")
		}
		return nil
	})
	if err := c.Session(context.Background(), "echo ok\n", statusPath); err != nil || n != 1 {
		t.Fatalf("happy path: %v n=%d", err, n)
	}

	called := false
	var log bytes.Buffer
	dry := New(&recorder{respond: func([]string, IO) error { called = true; return nil }}, Options{DryRun: true, Log: &log, Bin: "wslc"})
	if err := dry.Session(context.Background(), "echo ok\n", statusPath); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("dry-run executed the session")
	}
	if !strings.Contains(log.String(), dryRunNonce) || !strings.Contains(log.String(), "trap ") {
		t.Fatalf("dry-run output: %s", log.String())
	}
	if err := dry.Session(context.Background(), "echo ok\n", "relative"); err == nil {
		t.Fatal("bad status path accepted")
	}
}

func TestOtherCommandsDoNotRetryInvalidHandle(t *testing.T) {
	n := 0
	r := &recorder{respond: func([]string, IO) error {
		n++
		return &Error{Args: []string{"start"}, Code: 1, Stderr: "ERROR_INVALID_HANDLE"}
	}}
	c := New(r, Options{Backoff: time.Millisecond, Retries: 5})
	if err := c.Start(context.Background(), "x"); err == nil || n != 1 {
		t.Fatalf("ERROR_INVALID_HANDLE is only retried for session run: %v n=%d", err, n)
	}
}

func TestSessionTrapRecordsExit(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	status := filepath.ToSlash(filepath.Join(dir, "s.status"))
	run := func(body, nonce string) (int, string) {
		t.Helper()
		cmd := exec.Command(sh, "-c", wrapSessionScript(body, status, nonce))
		err := cmd.Run()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatal(err)
			}
			code = ee.ExitCode()
		}
		b, rerr := os.ReadFile(filepath.FromSlash(status))
		if rerr != nil {
			t.Fatal(rerr)
		}
		return code, string(b)
	}
	if code, body := run("set -eu\nfalse\n", "abc"); code != 1 || body != "abc 1\n" {
		t.Fatalf("false: code %d body %q", code, body)
	}
	if code, body := run("set -eu\nexit 0\n", "def"); code != 0 || body != "def 0\n" {
		t.Fatalf("zero: code %d body %q", code, body)
	}
	if code, body := run("set -eu\nexit 72\n", "aa"); code != 72 || body != "aa 72\n" {
		t.Fatalf("72: code %d body %q", code, body)
	}
}

func newSessionClient(t *testing.T, retries int, respond func([]string, IO) error) *Client {
	t.Helper()
	r := &recorder{respond: respond}
	return New(r, Options{Backoff: time.Millisecond, Log: io.Discard, Retries: retries})
}

func requireSessionIO(t *testing.T, stdio IO) {
	t.Helper()
	if stdio.Stdin == nil || stdio.Stdout == nil {
		t.Fatal("session must pass an empty stdin and a stdout buffer")
	}
	b, err := io.ReadAll(stdio.Stdin)
	if err != nil || len(b) != 0 {
		t.Fatalf("stdin must be empty, got %q %v", b, err)
	}
}

func sessionField(script, name string) string {
	key := name + "='"
	i := strings.Index(script, key)
	if i < 0 {
		return ""
	}
	rest := script[i+len(key):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return ""
	}
	return rest[:j]
}
