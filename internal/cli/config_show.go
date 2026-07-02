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
	flags := &overrideFlags{}
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Print the fully resolved configuration for a bothy",
		Long: "Print the fully resolved configuration for a bothy.\n\n" +
			"Accepts the same override flags as create, so the output previews\n" +
			"exactly what a create with those flags would resolve. Note that\n" +
			"drift detection on enter/run covers only the file layers (global\n" +
			"defaults + manifest), never one-off flags.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			overlay, err := flags.manifest()
			if err != nil {
				return err
			}
			res, err := resolveConfig(args[0], file, overlay, false)
			if err != nil {
				return err
			}
			out, err := yaml.Marshal(res.cfg)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(out)
			return err
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "manifest path (default ~/.config/bothy/<name>.yaml)")
	flags.register(cmd)
	return cmd
}
