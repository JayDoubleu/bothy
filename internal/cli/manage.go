package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jaydoubleu/bothy/internal/engine"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List bothies",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd.Context())
		},
	}
}

func runList(ctx context.Context) error {
	rt := newRuntime()
	if err := rt.Preflight(ctx); err != nil {
		return err
	}
	containers, err := rt.List(ctx, map[string]string{engine.LabelManaged: "true"})
	if err != nil {
		return err
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })

	// Write errors surface once, at Flush.
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tIMAGE\tSTATUS")
	for _, c := range containers {
		name := c.Labels[engine.LabelName]
		if name == "" {
			name = strings.TrimPrefix(c.Name, engine.ContainerPrefix)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", name, c.Image, c.Status)
	}
	return w.Flush()
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a bothy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd.Context(), args[0])
		},
	}
}

func runStop(ctx context.Context, name string) error {
	rt := newRuntime()
	if err := rt.Preflight(ctx); err != nil {
		return err
	}
	c, err := inspectBothy(ctx, rt, name)
	if err != nil {
		return err
	}
	if err := rt.Stop(ctx, c.Name); err != nil {
		return err
	}
	fmt.Printf("stopped %s\n", name)
	return nil
}

func newRmCmd() *cobra.Command {
	var keepHome, force bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a bothy and (by default) its home directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRm(cmd.Context(), args[0], keepHome, force)
		},
	}
	cmd.Flags().BoolVar(&keepHome, "keep-home", false, "keep the bothy's home directory")
	cmd.Flags().BoolVarP(&force, "force", "y", false, "do not ask for confirmation before deleting the home directory")
	return cmd
}

func runRm(ctx context.Context, name string, keepHome, force bool) error {
	rt := newRuntime()
	if err := rt.Preflight(ctx); err != nil {
		return err
	}
	c, err := inspectBothy(ctx, rt, name)
	if err != nil {
		return err
	}
	home := c.Labels[engine.LabelHome]

	if err := rt.Remove(ctx, c.Name, true); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", name)

	if keepHome {
		if home != "" {
			fmt.Printf("kept home directory %s\n", home)
		}
		return nil
	}
	if home == "" {
		fmt.Fprintf(os.Stderr, "warning: container had no %s label; not deleting any home directory\n", engine.LabelHome)
		return nil
	}
	if err := guardHomeDeletion(home); err != nil {
		return err
	}
	if _, err := os.Stat(home); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if !force && !confirm(fmt.Sprintf("Delete home directory %s?", home)) {
		fmt.Printf("kept home directory %s\n", home)
		return nil
	}
	if err := os.RemoveAll(home); err != nil {
		return fmt.Errorf("cannot delete home directory: %w", err)
	}
	fmt.Printf("deleted home directory %s\n", home)
	return nil
}

// guardHomeDeletion refuses to delete anything that cannot plausibly be a
// bothy home, whatever the label says.
func guardHomeDeletion(home string) error {
	if !filepath.IsAbs(home) || filepath.Clean(home) == "/" {
		return fmt.Errorf("refusing to delete suspicious home directory %q", home)
	}
	if real, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == filepath.Clean(real) {
		return fmt.Errorf("refusing to delete %s: it is your real home directory", home)
	}
	return nil
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func inspectBothy(ctx context.Context, rt runtime.Runtime, name string) (*runtime.Container, error) {
	c, err := rt.Inspect(ctx, engine.ContainerName(name))
	if errors.Is(err, runtime.ErrNotFound) {
		return nil, fmt.Errorf("no bothy named %q", name)
	}
	if err != nil {
		return nil, err
	}
	if err := requireManaged(c); err != nil {
		return nil, err
	}
	return c, nil
}

// requireManaged refuses to operate on containers that merely share the
// bothy- name prefix; the managed label is the source of truth.
func requireManaged(c *runtime.Container) error {
	if c.Labels[engine.LabelManaged] != "true" {
		return fmt.Errorf("container %s exists but was not created by bothy; refusing to touch it", c.Name)
	}
	return nil
}
