package cli

import (
	"github.com/spf13/cobra"

	"github.com/dawnmagnet/wslc-compose-go/internal/app"
)

func buildCmd(build func() *app.App) *cobra.Command {
	var o app.BuildOptions
	c := &cobra.Command{
		Use:   "build [SERVICE...]",
		Short: "Build service images",
		RunE: func(c *cobra.Command, args []string) error {
			o.Services = args
			return build().Build(c.Context(), o)
		},
	}
	c.Flags().BoolVar(&o.NoCache, "no-cache", false, "Do not use cache when building the image")
	c.Flags().BoolVar(&o.Pull, "pull", false, "Always attempt to pull a newer version of the base image")
	return c
}

func pullCmd(build func() *app.App) *cobra.Command {
	var o app.PullOptions
	c := &cobra.Command{
		Use:   "pull [SERVICE...]",
		Short: "Pull service images",
		RunE: func(c *cobra.Command, args []string) error {
			o.Services = args
			return build().Pull(c.Context(), o)
		},
	}
	c.Flags().BoolVar(&o.IgnoreFailures, "ignore-pull-failures", false, "Pull what it can and ignore images with pull failures")
	return c
}
