// Command wslc-compose is a Docker Compose compatible front end for the
// WSL Containers CLI (wslc).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/DawnMagnet/wslc-compose-go/internal/cli"
	"github.com/DawnMagnet/wslc-compose-go/internal/wslc"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := cli.NewRoot(nil)
	root.SetArgs(cli.Normalize(os.Args[1:]))
	err := root.ExecuteContext(ctx)
	if err == nil {
		return
	}
	var we *wslc.Error
	if errors.As(err, &we) && len(we.Args) > 0 && (we.Args[0] == "exec" || we.Args[0] == "run") && (we.Stderr == "" || we.Shown) {
		os.Exit(wslc.ExitCode(err)) // propagate the command's own exit status quietly
	}
	fmt.Fprintln(os.Stderr, "wslc-compose:", err)
	os.Exit(wslc.ExitCode(err))
}
