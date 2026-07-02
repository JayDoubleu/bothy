package cli

import (
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect bothy configuration",
	}
	cmd.AddCommand(newConfigShowCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Print the fully resolved configuration for a bothy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(args[0], file, nil, true)
			if err != nil {
				return err
			}
			out, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(out)
			return err
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "manifest path (default ~/.config/bothy/<name>.yaml)")
	return cmd
}
