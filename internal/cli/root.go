// Package cli defines the cobra command tree. Commands are thin: they parse
// flags into option structs and delegate to package app.
package cli

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dawnmagnet/wslc-compose-go/internal/app"
	"github.com/dawnmagnet/wslc-compose-go/internal/translate"
)

// Factory builds the App once global flags are parsed; tests may replace it.
type Factory func(app.Options) *app.App

// NewRoot returns the root command. factory nil means app.Default.
func NewRoot(factory Factory) *cobra.Command {
	if factory == nil {
		factory = app.Default
	}
	var (
		o    app.Options
		ansi string
	)
	root := &cobra.Command{
		Use:   "wslc-compose",
		Short: "Docker Compose for WSL Containers (wslc)",
		Long: "wslc-compose reads standard Compose files and drives the wslc CLI " +
			"(WSL Containers) to create networks, volumes, images and containers.\n" +
			"Every command supports --dry-run, which prints the wslc commands instead of running them.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       app.Version,
	}
	f := root.PersistentFlags()
	f.StringArrayVarP(&o.Files, "file", "f", nil, "Compose configuration files")
	f.StringVarP(&o.ProjectName, "project-name", "p", "", "Project name")
	f.StringVar(&o.ProjectDir, "project-directory", "", "Alternate working directory (default: directory of the first compose file)")
	f.StringArrayVar(&o.Profiles, "profile", nil, "Profiles to enable (or COMPOSE_PROFILES)")
	f.StringArrayVar(&o.EnvFiles, "env-file", nil, "Alternate environment files for interpolation")
	f.BoolVar(&o.DryRun, "dry-run", false, "Print wslc commands instead of executing them")
	f.BoolVar(&o.Strict, "strict", false, "Fail when the compose file uses features wslc cannot honor")
	f.StringVar(&o.Bin, "wslc", "", "Path to the wslc binary (default: $WSLC_COMPOSE_BIN, PATH, C:\\Program Files\\WSL\\wslc.exe)")
	f.StringSliceVar(&o.DefaultDNS, "default-dns", translate.DefaultDNS, "DNS servers for services without a dns: entry (empty string disables)")
	f.IntVar(&o.Parallel, "parallel", 1, "Max concurrent wslc queries")
	f.StringVar(&ansi, "ansi", "auto", "Colored output: auto|always|never")

	build := func() *app.App {
		o.DefaultDNS = nonEmpty(o.DefaultDNS)
		o.Color = ansi == "always" || (ansi == "auto" && isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "")
		return factory(o)
	}
	root.AddCommand(
		upCmd(build), downCmd(build), startCmd(build), stopCmd(build), restartCmd(build),
		psCmd(build), logsCmd(build), configCmd(build), versionCmd(build),
		buildCmd(build), pullCmd(build), execCmd(build), runCmd(build),
	)
	return root
}

func nonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// timeoutFlag registers -t/--timeout (seconds, -1 = wslc default).
func timeoutFlag(c *cobra.Command, v *int) {
	c.Flags().IntVarP(v, "timeout", "t", -1, "Shutdown timeout in seconds (-1: wslc default)")
}

const defaultWait = 5 * time.Minute

// globalWithValue lists global flags that consume the following argument.
var globalWithValue = map[string]bool{
	"-f": true, "--file": true, "-p": true, "--project-name": true, "--profile": true,
	"--env-file": true, "--project-directory": true, "--wslc": true,
	"--default-dns": true, "--parallel": true, "--ansi": true,
}

// Normalize rewrites `logs -f` to `logs --follow`. Docker Compose users type
// -f for both the global --file and logs --follow; cobra cannot register the
// same shorthand twice, so the subcommand's -f is disambiguated by position.
func Normalize(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		a := out[i]
		if globalWithValue[a] {
			i++
			continue
		}
		if len(a) > 0 && a[0] == '-' {
			continue
		}
		if a != "logs" {
			return out
		}
		for j := i + 1; j < len(out) && out[j] != "--"; j++ {
			if out[j] == "-f" {
				out[j] = "--follow"
			}
		}
		return out
	}
	return out
}
