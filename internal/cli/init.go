package cli

import (
	"github.com/spf13/cobra"

	"github.com/jaydoubleu/bothy/internal/engine"
)

// newInitCmd is the hidden container entrypoint. The bothy binary is bind
// mounted into every container and this subcommand runs as PID 1 there; it
// is never meant to be invoked by hand on the host.
func newInitCmd() *cobra.Command {
	var opts engine.InitOptions
	cmd := &cobra.Command{
		Use:    "init",
		Short:  "Container entrypoint (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return engine.RunInit(opts)
		},
	}
	flags := cmd.Flags()
	flags.IntVar(&opts.UID, "uid", 0, "user ID to create")
	flags.IntVar(&opts.GID, "gid", 0, "group ID to create")
	flags.StringVar(&opts.Username, "username", "", "username to create")
	flags.StringVar(&opts.Shell, "shell", "", "preferred login shell")
	flags.StringVar(&opts.Home, "home", "", "in-container home directory")
	flags.StringVar(&opts.Stamp, "stamp", "", "readiness stamp nonce")
	flags.StringArrayVar(&opts.Packages, "package", nil, "package to install at first boot (repeatable)")
	for _, name := range []string{"uid", "gid", "username", "home", "stamp"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}
