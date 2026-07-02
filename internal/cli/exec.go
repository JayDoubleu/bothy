package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jaydoubleu/bothy/internal/config"
	"github.com/jaydoubleu/bothy/internal/engine"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

const enterReadyTimeout = 60 * time.Second

// loginShellCommand resolves the user's shell from the container's passwd
// database at exec time, so enter works whatever shell init settled on.
const loginShellCommand = `shell="$(getent passwd "$(id -un)" | cut -d: -f7)"; [ -x "$shell" ] || shell=/bin/sh; export SHELL="$shell"; exec "$shell" -l`

func newEnterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enter <name>",
		Short: "Enter a bothy with an interactive login shell",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(), args[0], []string{"/bin/sh", "-c", loginShellCommand}, isTerminal(os.Stdin) && isTerminal(os.Stdout))
		},
	}
}

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name> -- <command> [args...]",
		Short: "Run a command inside a bothy",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(), args[0], args[1:], isTerminal(os.Stdin) && isTerminal(os.Stdout))
		},
	}
}

func runExec(ctx context.Context, name string, command []string, tty bool) error {
	rt := newRuntime()
	if err := rt.Preflight(ctx); err != nil {
		return err
	}
	c, err := ensureRunning(ctx, rt, name)
	if err != nil {
		return err
	}
	u, err := hostUser()
	if err != nil {
		return err
	}
	env := map[string]string{
		"HOME": engine.ContainerHome(u),
		"USER": u.Username,
	}
	if term := os.Getenv("TERM"); term != "" && tty {
		env["TERM"] = term
	}
	return rt.Exec(ctx, runtime.ExecSpec{
		Container:   c.Name,
		User:        u.Username,
		WorkDir:     engine.ContainerHome(u),
		Env:         env,
		Interactive: true,
		TTY:         tty,
		Command:     command,
	})
}

// ensureRunning inspects, warns on config drift, starts the container if
// needed and waits for the init readiness stamp.
func ensureRunning(ctx context.Context, rt runtime.Runtime, name string) (*runtime.Container, error) {
	c, err := rt.Inspect(ctx, engine.ContainerName(name))
	if errors.Is(err, runtime.ErrNotFound) {
		return nil, fmt.Errorf("no bothy named %q (create it with \"bothy create %s\")", name, name)
	}
	if err != nil {
		return nil, err
	}

	warnOnDrift(name, c)

	if !c.Running {
		if err := rt.Start(ctx, c.Name); err != nil {
			return nil, err
		}
	}

	home := c.Labels[engine.LabelHome]
	stamp := c.Labels[engine.LabelStamp]
	if home == "" || stamp == "" {
		return nil, fmt.Errorf("container %s is missing bothy labels; was it created by bothy?", c.Name)
	}
	waitCtx, cancel := context.WithTimeout(ctx, enterReadyTimeout)
	defer cancel()
	if err := engine.WaitReady(waitCtx, engine.ReadyStampPath(home, stamp)); err != nil {
		return nil, fmt.Errorf("%w\ncheck \"podman logs %s\" for details", err, c.Name)
	}
	return c, nil
}

// warnOnDrift compares the hash of the currently resolved config against
// the config-hash label baked in at create time. Best-effort: a bothy
// created from a manifest outside ~/.config/bothy cannot be checked.
func warnOnDrift(name string, c *runtime.Container) {
	path, err := config.DefaultManifestPath(name)
	if err != nil {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	cfg, err := resolveConfig(name, "", nil, true)
	if err != nil {
		return
	}
	hash, err := engine.ConfigHash(cfg)
	if err != nil {
		return
	}
	if hash != c.Labels[engine.LabelConfigHash] {
		fmt.Fprintf(os.Stderr,
			"warning: the configuration of %q has changed since this container was created;\n"+
				"         run \"bothy rm %s && bothy create %s\" to apply it\n",
			name, name, name)
	}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
