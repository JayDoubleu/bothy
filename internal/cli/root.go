// Package cli wires the bothy subcommands.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/jaydoubleu/bothy/internal/runtime"
)

// Version is stamped via -ldflags at release time.
var Version = "0.1.0-dev"

// newRuntime is a hook so tests can substitute runtime.Fake.
var newRuntime = func() runtime.Runtime {
	return runtime.NewPodman()
}

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "bothy",
		Short: "Isolated, declarative containerised development environments",
		Long: `bothy manages containerised development environments on rootless podman.

Each bothy is declared in a YAML manifest and gets its own private home
directory: nothing from your real home crosses in unless the manifest
declares it.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newCreateCmd(),
		newEnterCmd(),
		newRunCmd(),
		newListCmd(),
		newStopCmd(),
		newRmCmd(),
		newConfigCmd(),
		newInitCmd(),
	)
	return root
}
