package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

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
		"HOME":           engine.ContainerHome(u),
		"USER":           u.Username,
		engine.EnvMarker: name,
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
	if err := requireManaged(c); err != nil {
		return nil, err
	}

	warnOnDrift(name, c)

	home := c.Labels[engine.LabelHome]
	stamp := c.Labels[engine.LabelStamp]
	if home == "" || stamp == "" {
		return nil, fmt.Errorf("container %s is missing bothy labels; was it created by bothy?", c.Name)
	}
	stampPath := engine.ReadyStampPath(home, stamp)

	if !c.Running {
		// The stamp on disk is from the previous boot; remove it so
		// readiness gates on this boot's init actually finishing.
		if err := os.Remove(stampPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cannot clear stale readiness stamp: %w", err)
		}
		if err := rt.Start(ctx, c.Name); err != nil {
			return nil, err
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, enterReadyTimeout)
	defer cancel()
	alive := func() (bool, error) {
		cur, err := rt.Inspect(ctx, c.Name)
		if err != nil {
			return false, err
		}
		return cur.Running, nil
	}
	if err := engine.WaitReady(waitCtx, stampPath, alive); err != nil {
		return nil, fmt.Errorf("%w\ncheck \"podman logs %s\" for details", err, c.Name)
	}
	return c, nil
}

// warnOnDrift recomputes the hash of the declarative config layers (global
// defaults + the manifest file recorded at create time) and compares it to
// the config-hash label. Best-effort by design: no manifest label (an
// --image-only create) or a manifest that no longer loads means nothing to
// compare, and the check is silently skipped.
func warnOnDrift(name string, c *runtime.Container) {
	manifestPath := c.Labels[engine.LabelManifest]
	if manifestPath == "" {
		return
	}
	globalPath, err := config.GlobalConfigPath()
	if err != nil {
		return
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		return
	}
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return
	}
	var defaults *config.Manifest
	if global != nil {
		defaults = global.Defaults
	}
	hash, err := engine.ManifestHash(defaults, manifest)
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
	return term.IsTerminal(int(f.Fd()))
}
