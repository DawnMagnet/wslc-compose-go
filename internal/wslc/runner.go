// Package wslc is a typed client for the wslc CLI (WSL Containers).
//
// All interaction goes through the Runner interface so that the rest of the
// program can be tested without a real wslc binary, and so that --dry-run is
// a property of the client rather than of every caller.
package wslc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// IO bundles the standard streams for one invocation. Nil streams are discarded.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Runner executes one wslc command line (args exclude the binary itself).
type Runner interface {
	Run(ctx context.Context, args []string, stdio IO) error
}

// RunnerFunc adapts a function to the Runner interface.
type RunnerFunc func(ctx context.Context, args []string, stdio IO) error

// Run implements Runner.
func (f RunnerFunc) Run(ctx context.Context, args []string, stdio IO) error {
	return f(ctx, args, stdio)
}

// Error describes a failed wslc invocation.
type Error struct {
	Args   []string
	Code   int
	Stderr string
	Err    error
	// Shown is set when Stderr was already streamed to the user's terminal;
	// Error() then omits it so failures are not printed twice.
	Shown bool
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("wslc %s: exit %d", strings.Join(head(e.Args, 2), " "), e.Code)
	var ee *exec.ExitError
	switch {
	case e.Stderr != "" && !e.Shown:
		msg += ": " + e.Stderr
	case e.Stderr == "" && e.Err != nil && !errors.As(e.Err, &ee):
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode extracts the process exit code from err (1 when unknown, 0 for nil).
func ExitCode(err error) int {
	var we *Error
	if errors.As(err, &we) && we.Code > 0 {
		return we.Code
	}
	if err != nil {
		return 1
	}
	return 0
}

// Exec runs the real wslc binary.
type Exec struct{ Bin string }

// Run implements Runner using os/exec. Stderr is always teed into the error.
func (x Exec) Run(ctx context.Context, args []string, stdio IO) error {
	cmd := exec.CommandContext(ctx, x.Bin, args...)
	cmd.Stdin, cmd.Stdout = stdio.Stdin, stdio.Stdout
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if stdio.Stderr != nil {
		cmd.Stderr = io.MultiWriter(stdio.Stderr, &errBuf)
	}
	err := cmd.Run()
	if err == nil {
		return nil
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	return &Error{Args: args, Code: code, Stderr: strings.TrimSpace(errBuf.String()), Err: err, Shown: stdio.Stderr != nil}
}

// Fallback locations of wslc.exe when it is not on PATH.
var fallbacks = []string{
	`C:\Program Files\WSL\wslc.exe`,
	"/mnt/c/Program Files/WSL/wslc.exe",
}

// Find resolves the wslc binary: explicit value, $WSLC_COMPOSE_BIN, PATH
// (wslc.exe before wslc), then the default install location. It never
// returns an error; an unresolved name is reported when first executed.
func Find(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("WSLC_COMPOSE_BIN"); env != "" {
		return env
	}
	names := []string{"wslc.exe", "wslc"}
	if runtime.GOOS == "windows" {
		names = names[:1]
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "wslc"
}

func head(s []string, n int) []string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
