package wslc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
// "<nonce>-<attempt> <exit>" to statusPath (for example /run/<owner>.status).
// On that error the client reads the file with `cat` and uses only the
// record for this attempt. A previous attempt's success cannot mask a later
// failure. The full script is run again only when this attempt's record is
// absent. ERROR_SHARING_VIOLATION and ERROR_ALREADY_EXISTS are retried the
// same way as other wslc mutations. Install and cleanup are idempotent
// under flock, and `cat` only reads.
//
// Stdin is an explicit empty reader and stdout is a buffer, so the process
// handles are pipes rather than the inherited or null device.
func (c *Client) Session(ctx context.Context, script, statusPath string) error {
	if err := validateStatusPath(statusPath); err != nil {
		return err
	}
	base := dryRunNonce
	if !c.o.DryRun {
		base = randomNonce()
	}
	if !validNonce(base) {
		return fmt.Errorf("wslc system session: invalid nonce %q", base)
	}
	if c.o.DryRun {
		args := []string{"system", "session", "run", "sh", "-c", wrapSessionScript(script, statusPath, attemptNonce(base, 1))}
		fmt.Fprintln(c.o.Log, Format(c.o.Bin, args))
		return nil
	}
	defer c.acquire()()
	var last error
	for attempt := 1; attempt <= c.o.Retries; attempt++ {
		nonce := attemptNonce(base, attempt)
		args := []string{"system", "session", "run", "sh", "-c", wrapSessionScript(script, statusPath, nonce)}
		err := c.runSession(ctx, args)
		if err == nil {
			return nil
		}
		handle := matches(err, []string{invalidHandle})
		if !handle && !matches(err, transient) {
			return err
		}
		last = err
		if handle {
			code, found, readErr := c.readSessionStatus(ctx, statusPath, nonce)
			if readErr != nil {
				return fmt.Errorf("wslc system session: ERROR_INVALID_HANDLE and %s could not be read after %d attempts: %w", statusPath, c.o.Retries, readErr)
			}
			if found {
				fmt.Fprintf(c.o.Log, "wslc system session: ERROR_INVALID_HANDLE after the script ran; %s says exit %d\n", statusPath, code)
				if code == 0 {
					return nil
				}
				return sessionStatusError(code, err)
			}
		}
		if attempt == c.o.Retries {
			if handle {
				return fmt.Errorf("wslc system session: ERROR_INVALID_HANDLE and %s has no status for this attempt after %d attempts: %w", statusPath, c.o.Retries, err)
			}
			return err
		}
		why := invalidHandle
		if !handle {
			why = "transient error"
		}
		fmt.Fprintf(c.o.Log, "wslc system session: %s, retrying (%d/%d)\n", why, attempt, c.o.Retries-1)
		if err := c.sleep(ctx); err != nil {
			return err
		}
	}
	return last
}

// sessionStatusError reports the script's own exit code and keeps the stderr
// wslc already captured (the script's output, plus the handle error).
func sessionStatusError(code int, cause error) error {
	stderr := fmt.Sprintf("script exit %d", code)
	var we *Error
	if errors.As(cause, &we) && strings.TrimSpace(we.Stderr) != "" {
		stderr = fmt.Sprintf("script exit %d\n%s", code, strings.TrimSpace(we.Stderr))
	}
	return &Error{Args: []string{"system", "session", "run"}, Code: code, Stderr: stderr, Err: cause}
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

// wrapSessionScript prepends signal traps and an EXIT trap. Bash records $?
// as 0 when a signal ends the shell unless the signal trap exits with the
// conventional code first; dash and busybox ash then see that code too.
// The status line is written to a temp file and renamed into place so two
// overlapping runs cannot leave a partial record.
func wrapSessionScript(script, statusPath, nonce string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "_wcg_nonce='%s'\n_wcg_status='%s'\n", nonce, statusPath)
	b.WriteString("trap 'exit 143' TERM\n")
	b.WriteString("trap 'exit 130' INT\n")
	b.WriteString("trap 'exit 129' HUP\n")
	// set -e is still on inside the trap. A failing mv is the last command of
	// the && list, and a following rm can fail too once the directory is not
	// writable. Both sit in an || list so neither can replace $_wcg_rc.
	b.WriteString("trap '_wcg_rc=$?; _wcg_tmp=\"${_wcg_status}.${_wcg_nonce}\"; { printf \"%s %s\\n\" \"$_wcg_nonce\" \"$_wcg_rc\" > \"$_wcg_tmp\" && mv -f \"$_wcg_tmp\" \"$_wcg_status\"; } 2>/dev/null || rm -f \"$_wcg_tmp\" 2>/dev/null || :; exit $_wcg_rc' EXIT\n")
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

func attemptNonce(base string, attempt int) string {
	return fmt.Sprintf("%s-%d", base, attempt)
}

func validNonce(n string) bool {
	if i := strings.IndexByte(n, '-'); i >= 0 {
		rest := n[i+1:]
		if rest == "" || strings.Trim(rest, "0123456789") != "" {
			return false
		}
		n = n[:i]
	}
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
