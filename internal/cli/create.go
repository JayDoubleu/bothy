package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jaydoubleu/bothy/internal/engine"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

// Package installs at first boot can take a while; give them room.
const (
	createReadyTimeout         = 60 * time.Second
	createReadyTimeoutPackages = 5 * time.Minute
)

// newStamp is a hook so tests can use a deterministic readiness stamp.
var newStamp = engine.NewStamp

func newCreateCmd() *cobra.Command {
	var file string
	flags := &overrideFlags{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create and initialize a new bothy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd.Context(), args[0], file, flags)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "manifest path (default ~/.config/bothy/<name>.yaml)")
	flags.register(cmd)
	return cmd
}

func runCreate(ctx context.Context, name, file string, flags *overrideFlags) error {
	if err := validateName(name); err != nil {
		return err
	}
	rt := newRuntime()
	if err := rt.Preflight(ctx); err != nil {
		return err
	}

	containerName := engine.ContainerName(name)
	if c, err := rt.Inspect(ctx, containerName); err == nil {
		if c.Labels[engine.LabelManaged] == "true" {
			return fmt.Errorf("bothy %q already exists (remove it with \"bothy rm %s\")", name, name)
		}
		return fmt.Errorf("container name %q is taken by a container bothy does not manage; pick another bothy name", containerName)
	} else if !errors.Is(err, runtime.ErrNotFound) {
		return err
	}

	overlay, err := flags.manifest()
	if err != nil {
		return err
	}
	res, err := resolveConfig(name, file, overlay, false)
	if err != nil {
		return err
	}
	cfg := res.cfg

	u, err := hostUser()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Home, 0o700); err != nil {
		return fmt.Errorf("cannot create home directory: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the bothy binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	stamp, err := newStamp()
	if err != nil {
		return err
	}
	hash, err := engine.ManifestHash(res.defaults, res.manifest)
	if err != nil {
		return err
	}
	meta := engine.CreateMeta{ManifestPath: res.manifestPath, ManifestHash: hash}
	spec, warnings, err := engine.BuildCreateSpec(name, cfg, exe, u, stamp, meta)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}

	hasOverlay := false
	for _, m := range spec.Mounts {
		if !m.Overlay {
			continue
		}
		hasOverlay = true
		for _, dir := range []string{m.UpperDir, m.WorkDir} {
			if dir == "" {
				continue
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("cannot create overlay directory: %w", err)
			}
		}
	}

	if _, err := rt.Create(ctx, spec); err != nil {
		if hasOverlay && strings.Contains(err.Error(), "Invalid argument") {
			return fmt.Errorf("%w\nhint: an overlay mount's source must not contain other active mounts (podman's own storage under ~/.local/share/containers rules out whole-home overlays), and the bothy home must not live inside an overlay source", err)
		}
		return err
	}
	if err := rt.Start(ctx, containerName); err != nil {
		return err
	}

	timeout := createReadyTimeout
	if len(cfg.Packages) > 0 {
		timeout = createReadyTimeoutPackages
		fmt.Fprintf(os.Stderr, "installing packages: %v\n", cfg.Packages)
	}
	fmt.Fprintln(os.Stderr, "waiting for container initialization...")
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	alive := func() (bool, error) {
		c, err := rt.Inspect(ctx, containerName)
		if err != nil {
			return false, err
		}
		return c.Running, nil
	}
	if err := engine.WaitReady(waitCtx, engine.ReadyStampPath(cfg.Home, stamp), alive); err != nil {
		return fmt.Errorf("%w\ncheck \"podman logs %s\" for details", err, containerName)
	}

	fmt.Printf("created %s (enter it with \"bothy enter %s\")\n", name, name)
	return nil
}
