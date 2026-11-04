package wslc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// invalidHandle is the locale-independent token wslc 3.0.3.0 prints when
// `system session run` loses a process handle. The Chinese text ("句柄无效")
// is not matched. The script has usually already finished; its own exit
// status is what decides the result.
const invalidHandle = "ERROR_INVALID_HANDLE"

// dryRunNonce is printed instead of a random nonce. Dry-run does not execute
// the script, so the command line stays stable.
const dryRunNonce = "0000000000000000"

// Session runs a script inside the wslc utility VM (`system session run`).
//
// wslc 3.0.3.0 intermittently exits 1 with ERROR_INVALID_HANDLE after the
// script has already run. The script is wrapped so an EXIT trap writes
// "<nonce> <exit>" to statusPath (for example /run/<owner>.status). On that
// error the client reads the file with `cat` and uses the recorded status
// for this nonce. The full script is run again only when that nonce is
// absent. Both the gateway script and the read are idempotent: install and
// cleanup take a flock and replace or delete chains in place, and `cat`
// only reads.
//
// Stdin is an explicit empty reader and stdout is a buffer, so the process
// handles are pipes rather than the inherited or null device.
func (c *Client) Session(ctx context.Context, script, statusPath string) error {
	if err := validateStatusPath(statusPath); err != nil {
		return err
	}
	nonce := dryRunNonce
	if !c.o.DryRun {
		nonce = randomNonce()
	}
	if !validNonce(nonce) {
		return fmt.Errorf("wslc system session: invalid nonce %q", nonce)
	}
	wrapped := wrapSessionScript(script, statusPath, nonce)
	args := []string{"system", "session", "run", "sh", "-c", wrapped}
	if c.o.DryRun {
		fmt.Fprintln(c.o.Log, Format(c.o.Bin, args))
		return nil
	}
	defer c.acquire()()
	var last error
	for attempt := 1; attempt <= c.o.Retries; attempt++ {
		err := c.runSession(ctx, args)
		if err == nil {
			return nil
		}
		if !matches(err, []string{invalidHandle}) {
			return err
		}
		last = err
		code, found, readErr := c.readSessionStatus(ctx, statusPath, nonce)
		if readErr != nil {
			return fmt.Errorf("wslc system session: ERROR_INVALID_HANDLE and %s could not be read after %d attempts: %w", statusPath, c.o.Retries, readErr)
		}
		if found {
			fmt.Fprintf(c.o.Log, "wslc system session: ERROR_INVALID_HANDLE after the script ran; %s says exit %d\n", statusPath, code)
			if code == 0 {
				return nil
			}
			return &Error{Args: []string{"system", "session", "run"}, Code: code, Stderr: fmt.Sprintf("script exit %d", code)}
		}
		if attempt == c.o.Retries {
			break
		}
		fmt.Fprintf(c.o.Log, "wslc system session: ERROR_INVALID_HANDLE and %s has no status for this attempt, retrying (%d/%d)\n", statusPath, attempt, c.o.Retries-1)
		if err := c.sleep(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("wslc system session: ERROR_INVALID_HANDLE and %s has no status for this attempt after %d attempts: %w", statusPath, c.o.Retries, last)
}

// runSession executes one session command with an empty stdin and a captured stdout.
func (c *Client) runSession(ctx context.Context, args []string) error {
	var out bytes.Buffer
	return c.r.Run(ctx, args, IO{Stdin: strings.NewReader(""), Stdout: &out})
}

// readSessionStatus runs `cat <statusPath>` and looks for nonce. A real cat
// failure (the file is absent) or a clean read that does not contain nonce
// reports found=false so the caller can run the script again. ERROR_INVALID_HANDLE
// with no stdout is retried; stdout is still accepted, because wslc often
// prints the output and then fails while reporting the exit code. A non-nil
// error means the file could not be read at all.
func (c *Client) readSessionStatus(ctx context.Context, statusPath, nonce string) (code int, found bool, err error) {
	args := []string{"system", "session", "run", "sh", "-c", "cat " + statusPath}
	var last error
	for attempt := 1; attempt <= c.o.Retries; attempt++ {
		var out bytes.Buffer
		err := c.r.Run(ctx, args, IO{Stdin: strings.NewReader(""), Stdout: &out})
		if code, ok := parseSessionStatus(out.String(), nonce); ok {
			return code, true, nil
		}
		if err == nil || !matches(err, []string{invalidHandle}) || strings.TrimSpace(out.String()) != "" {
			return 0, false, nil
		}
		last = err
		if attempt == c.o.Retries {
			break
		}
		fmt.Fprintf(c.o.Log, "wslc system session: ERROR_INVALID_HANDLE reading %s, retrying (%d/%d)\n", statusPath, attempt, c.o.Retries-1)
		if err := c.sleep(ctx); err != nil {
			return 0, false, err
		}
	}
	return 0, false, last
}

func (c *Client) sleep(ctx context.Context) error {
	timer := time.NewTimer(c.o.Backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// wrapSessionScript prepends an EXIT trap. The trap records the script's
// exit status, including failures from `set -e`, then re-exits with it so a
// clean wslc still propagates the real code.
func wrapSessionScript(script, statusPath, nonce string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "_wcg_nonce='%s'\n_wcg_status='%s'\n", nonce, statusPath)
	b.WriteString("trap '_wcg_rc=$?; printf \"%s %s\\n\" \"$_wcg_nonce\" \"$_wcg_rc\" > \"$_wcg_status\" 2>/dev/null || true; exit $_wcg_rc' EXIT\n")
	b.WriteString(script)
	if !strings.HasSuffix(script, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func parseSessionStatus(text, nonce string) (int, bool) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != nonce {
			continue
		}
		code, err := strconv.Atoi(fields[1])
		if err != nil || code < 0 || code > 255 {
			continue
		}
		return code, true
	}
	return 0, false
}

func validateStatusPath(p string) error {
	rest, ok := strings.CutPrefix(p, "/run/")
	if !ok || !strings.HasSuffix(rest, ".status") {
		return fmt.Errorf("wslc system session: invalid status path %q", p)
	}
	name := strings.TrimSuffix(rest, ".status")
	if name == "" || strings.ContainsAny(name, "/ \t\n'\"\\$`;|&<>()*?") {
		return fmt.Errorf("wslc system session: invalid status path %q", p)
	}
	return nil
}

func validNonce(n string) bool {
	if n == "" || len(n) > 64 {
		return false
	}
	for _, r := range n {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func randomNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
