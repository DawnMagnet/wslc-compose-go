package cli

import (
	"github.com/spf13/cobra"

	"github.com/dawnmagnet/wslc-compose-go/internal/app"
)

func psCmd(build func() *app.App) *cobra.Command {
	var o app.PsOptions
	c := &cobra.Command{
		Use:     "ps [SERVICE...]",
		Aliases: []string{"ls"},
		Short:   "List containers",
		RunE: func(c *cobra.Command, args []string) error {
			o.Services = args
			return build().Ps(c.Context(), o)
		},
	}
	c.Flags().BoolVarP(&o.All, "all", "a", false, "Show all containers, including stopped ones")
	c.Flags().BoolVarP(&o.Quiet, "quiet", "q", false, "Only display IDs")
	c.Flags().BoolVar(&o.ListServices, "services", false, "Display services")
	c.Flags().StringArrayVar(&o.Status, "status", nil, "Filter services by status: created|running|exited|... (repeatable)")
	c.Flags().StringVar(&o.Format, "format", "table", "Output format: table|json")
	return c
}

func logsCmd(build func() *app.App) *cobra.Command {
	var o app.LogsOptions
	c := &cobra.Command{
		Use:   "logs [SERVICE...]",
		Short: "View output from containers",
		RunE: func(c *cobra.Command, args []string) error {
			o.Services = args
			return build().Logs(c.Context(), o)
		},
	}
	f := c.Flags()
	f.BoolVar(&o.Follow, "follow", false, "Follow log output (-f is accepted after `logs`)")
	f.StringVarP(&o.Tail, "tail", "n", "all", "Number of lines to show from the end of the logs")
	f.BoolVarP(&o.Timestamps, "timestamps", "t", false, "Show timestamps")
	f.StringVar(&o.Since, "since", "", "Show logs since timestamp or relative duration")
	f.StringVar(&o.Until, "until", "", "Show logs before timestamp or relative duration")
	f.BoolVar(&o.NoPrefix, "no-log-prefix", false, "Don't print the container name prefix")
	return c
}

func configCmd(build func() *app.App) *cobra.Command {
	var o app.ConfigOptions
	c := &cobra.Command{
		Use:     "config",
		Aliases: []string{"convert"},
		Short:   "Parse, resolve and render the compose file in canonical format",
		Args:    cobra.NoArgs,
		RunE:    func(c *cobra.Command, _ []string) error { return build().Config(c.Context(), o) },
	}
	f := c.Flags()
	f.BoolVar(&o.Services, "services", false, "Print the service names, one per line")
	f.BoolVar(&o.Volumes, "volumes", false, "Print the volume names, one per line")
	f.BoolVar(&o.Networks, "networks", false, "Print the network names, one per line")
	f.BoolVar(&o.Images, "images", false, "Print the image names, one per line")
	f.StringVar(&o.Hash, "hash", "", `Print the config hash of services ("*" for all, or comma separated)`)
	f.StringVar(&o.Format, "format", "yaml", "Output format: yaml|json")
	return c
}

func versionCmd(build func() *app.App) *cobra.Command {
	var short bool
	c := &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		Run:   func(c *cobra.Command, _ []string) { build().PrintVersion(c.Context(), short) },
	}
	c.Flags().BoolVar(&short, "short", false, "Print only the version number")
	return c
}
