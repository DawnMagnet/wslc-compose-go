package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DawnMagnet/wslc-compose-go/internal/app"
)

func execCmd(build func() *app.App) *cobra.Command {
	var (
		o     app.ExecOptions
		noTTY bool
	)
	c := &cobra.Command{
		Use:   "exec [OPTIONS] SERVICE COMMAND [ARGS...]",
		Short: "Execute a command in a running container",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			o.Service, o.Command = args[0], dashDash(args[1:])
			if len(o.Command) == 0 {
				return fmt.Errorf("exec %s: missing command", o.Service)
			}
			if !c.Flags().Changed("tty") {
				o.TTY = isTerminal(os.Stdin) && !o.Detach
			}
			if noTTY {
				o.TTY = false
			}
			if o.Detach {
				o.Interactive = false
			}
			return build().Exec(c.Context(), o)
		},
	}
	c.Flags().SetInterspersed(false)
	f := c.Flags()
	f.BoolVarP(&o.Interactive, "interactive", "i", true, "Keep STDIN open")
	f.BoolVarP(&o.TTY, "tty", "t", false, "Allocate a pseudo-TTY (default: when stdin is a terminal)")
	f.BoolVarP(&noTTY, "no-tty", "T", false, "Disable pseudo-TTY allocation")
	f.BoolVarP(&o.Detach, "detach", "d", false, "Run the command in the background")
	f.StringVarP(&o.User, "user", "u", "", "Run the command as this user")
	f.StringVarP(&o.Workdir, "workdir", "w", "", "Working directory inside the container")
	f.StringArrayVarP(&o.Env, "env", "e", nil, "Set environment variables")
	f.IntVar(&o.Index, "index", 1, "Index of the container if the service has multiple replicas")
	return c
}

func runCmd(build func() *app.App) *cobra.Command {
	var (
		o          app.RunOptions
		entrypoint string
	)
	c := &cobra.Command{
		Use:   "run [OPTIONS] SERVICE [COMMAND] [ARGS...]",
		Short: "Run a one-off command on a service",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			o.Service, o.Command = args[0], dashDash(args[1:])
			if c.Flags().Changed("entrypoint") {
				o.Entrypoint = strings.Fields(entrypoint)
			}
			if !isTerminal(os.Stdin) {
				o.NoTTY = true
			}
			return build().Run(c.Context(), o)
		},
	}
	c.Flags().SetInterspersed(false)
	f := c.Flags()
	f.StringVar(&o.Name, "name", "", "Assign a name to the container")
	f.BoolVar(&o.Remove, "rm", false, "Automatically remove the container when it exits")
	f.BoolVarP(&o.Detach, "detach", "d", false, "Run container in background")
	f.BoolVar(&o.NoDeps, "no-deps", false, "Don't start linked services")
	f.BoolVar(&o.ServicePorts, "service-ports", false, "Publish the service's ports")
	f.BoolVarP(&o.NoTTY, "no-tty", "T", false, "Disable pseudo-TTY allocation")
	f.StringVarP(&o.User, "user", "u", "", "Run as specified username or uid")
	f.StringVarP(&o.Workdir, "workdir", "w", "", "Working directory inside the container")
	f.StringVar(&entrypoint, "entrypoint", "", "Override the entrypoint of the image")
	f.StringArrayVarP(&o.Env, "env", "e", nil, "Set environment variables")
	f.BoolVar(&o.Build, "build", false, "Build image before starting container")
	return c
}

// dashDash drops a leading "--" separator (`exec web -- ls -la`). Flag
// parsing stops at SERVICE, so cobra leaves it in place; wslc would treat
// it as the command name.
func dashDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}
