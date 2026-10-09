package cli

import (
	"github.com/spf13/cobra"

	"github.com/dawnmagnet/wslc-compose-go/internal/app"
)

func upCmd(build func() *app.App) *cobra.Command {
	var o app.UpOptions
	c := &cobra.Command{
		Use:   "up [SERVICE...]",
		Short: "Create and start containers",
		Long: "Create networks and volumes, build or pull images, then create or recreate containers\n" +
			"in dependency order. Containers whose configuration hash is unchanged are kept.",
		RunE: func(c *cobra.Command, args []string) error {
			o.Services = args
			return build().Up(c.Context(), o)
		},
	}
	f := c.Flags()
	f.BoolVarP(&o.Detach, "detach", "d", false, "Run containers in the background")
	f.BoolVar(&o.Build, "build", false, "Build images before starting containers")
	f.BoolVar(&o.NoBuild, "no-build", false, "Don't build an image, even if it's missing")
	f.StringVar(&o.Pull, "pull", "", "Pull image before running (always|missing|never)")
	f.BoolVar(&o.ForceRecreate, "force-recreate", false, "Recreate containers even if their configuration hasn't changed")
	f.BoolVar(&o.NoRecreate, "no-recreate", false, "Never recreate existing containers")
	f.BoolVar(&o.NoDeps, "no-deps", false, "Don't start linked services")
	f.BoolVar(&o.RemoveOrphans, "remove-orphans", false, "Remove containers for services not defined in the Compose file")
	f.BoolVar(&o.Wait, "wait", false, "Wait for services to be running|healthy (implies -d)")
	f.DurationVar(&o.WaitTimeout, "wait-timeout", defaultWait, "Maximum duration to wait for dependencies / --wait")
	timeoutFlag(c, &o.Timeout)
	c.MarkFlagsMutuallyExclusive("force-recreate", "no-recreate")
	c.MarkFlagsMutuallyExclusive("build", "no-build")
	c.PreRun = func(*cobra.Command, []string) {
		if o.Wait {
			o.Detach = true
		}
	}
	return c
}

func downCmd(build func() *app.App) *cobra.Command {
	var o app.DownOptions
	c := &cobra.Command{
		Use:   "down",
		Short: "Stop and remove containers and networks",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return build().Down(c.Context(), o) },
	}
	c.Flags().BoolVarP(&o.Volumes, "volumes", "v", false, "Remove named volumes declared in the compose file")
	c.Flags().BoolVar(&o.RemoveOrphans, "remove-orphans", false, "Remove containers for services not defined in the Compose file")
	timeoutFlag(c, &o.Timeout)
	return c
}

func startCmd(build func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "start [SERVICE...]",
		Short: "Start existing containers",
		RunE:  func(c *cobra.Command, args []string) error { return build().Start(c.Context(), args) },
	}
}

func stopCmd(build func() *app.App) *cobra.Command {
	var timeout int
	c := &cobra.Command{
		Use:   "stop [SERVICE...]",
		Short: "Stop running containers without removing them",
		RunE:  func(c *cobra.Command, args []string) error { return build().Stop(c.Context(), args, timeout) },
	}
	timeoutFlag(c, &timeout)
	return c
}

func restartCmd(build func() *app.App) *cobra.Command {
	var timeout int
	c := &cobra.Command{
		Use:   "restart [SERVICE...]",
		Short: "Restart containers (stop + start; wslc has no native restart)",
		RunE:  func(c *cobra.Command, args []string) error { return build().Restart(c.Context(), args, timeout) },
	}
	timeoutFlag(c, &timeout)
	return c
}
