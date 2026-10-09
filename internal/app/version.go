package app

import (
	"context"
	"fmt"
	"runtime"
)

// Build metadata, overridden via -ldflags "-X .../internal/app.Version=...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// PrintVersion writes tool and wslc version information.
func (a *App) PrintVersion(ctx context.Context, short bool) {
	if short {
		fmt.Fprintln(a.io.Stdout, Version)
		return
	}
	fmt.Fprintf(a.io.Stdout, "wslc-compose %s (commit %s, built %s, %s %s/%s)\n",
		Version, Commit, Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if v := a.c.Version(ctx); v != "" {
		fmt.Fprintf(a.io.Stdout, "wslc: %s\n", v)
	} else {
		fmt.Fprintf(a.io.Stdout, "wslc: not found (binary %q)\n", a.o.Bin)
	}
}
