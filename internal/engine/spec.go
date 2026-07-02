// Package engine turns resolved configurations into container specs and
// implements the lifecycle glue between the CLI and the runtime, including
// the in-container init process.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jaydoubleu/bothy/internal/config"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

// Container labels. Labels, not the name prefix, are the source of truth
// for which containers bothy manages.
const (
	labelPrefix        = "com.github.jaydoubleu.bothy."
	LabelManaged       = labelPrefix + "managed"
	LabelName          = labelPrefix + "name"
	LabelSchemaVersion = labelPrefix + "schema-version"
	LabelHome          = labelPrefix + "home"
	LabelConfigHash    = labelPrefix + "config-hash"
	// LabelStamp holds the readiness-stamp nonce generated at create time.
	// The init process writes ready-<nonce> into the bothy home and
	// create/enter/run poll for it. A nonce is used rather than the
	// container ID because init cannot learn its own ID (rootless podman
	// only writes id= into /run/.containerenv for privileged containers),
	// and a fresh nonce per create also defeats stale stamps left in a
	// home that was kept with rm --keep-home.
	LabelStamp = labelPrefix + "stamp"
)

// ContainerPrefix is prepended to the bothy name to form the container
// name; it only avoids clashes with unmanaged containers.
const ContainerPrefix = "bothy-"

// BinaryMountPath is where the bothy binary is bind mounted (read-only)
// inside every container so it can serve as the init entrypoint.
const BinaryMountPath = "/usr/libexec/bothy"

// ContainerName returns the container name for a bothy name.
func ContainerName(name string) string {
	return ContainerPrefix + name
}

// User describes the host user replicated inside the bothy.
type User struct {
	Username string
	UID      int
	GID      int
	Shell    string
}

// ContainerHome is the bothy home mount point inside the container.
func ContainerHome(u User) string {
	return "/home/" + u.Username
}

// ConfigHash returns the sha256 of the resolved config in its canonical
// serialization (JSON: fixed struct field order, sorted map keys). It is
// stored in the config-hash label and compared on enter/run to detect
// drift between the manifest and the running container.
func ConfigHash(cfg *config.Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("cannot serialize config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// BuildCreateSpec maps a resolved config to a runtime CreateSpec. exePath
// is the bothy binary on the host (mounted in as the init entrypoint) and
// stamp is the readiness-stamp nonce (see LabelStamp). The returned
// warnings are user-facing notes, currently one per enabled but not yet
// implemented integration toggle.
func BuildCreateSpec(name string, cfg *config.Config, exePath string, u User, stamp string) (runtime.CreateSpec, []string, error) {
	hash, err := ConfigHash(cfg)
	if err != nil {
		return runtime.CreateSpec{}, nil, err
	}

	var warnings []string
	mounts := []runtime.Mount{
		// The bothy's private home. The real host home is never mounted.
		{Source: cfg.Home, Target: ContainerHome(u)},
		{Source: exePath, Target: BinaryMountPath, ReadOnly: true},
	}
	overlays := 0
	for _, m := range cfg.Mounts {
		mount := runtime.Mount{Source: m.Source, Target: m.Target}
		switch m.Mode {
		case config.ModeRO:
			mount.ReadOnly = true
		case config.ModeOverlay:
			mount.Overlay = true
			mount.UpperDir, mount.WorkDir = OverlayDirs(cfg.Home, m.Target)
			overlays++
		}
		mounts = append(mounts, mount)
	}
	if overlays > 0 {
		warnings = append(warnings, "overlay mounts give the bothy read access to everything under their sources; only writes are contained")
	}

	integMounts, integEnv, integWarnings := integrationMounts(cfg)
	warnings = append(warnings, integWarnings...)
	mounts = append(mounts, integMounts...)

	env := make(map[string]string, len(cfg.Env)+len(integEnv)+1)
	for k, v := range cfg.Env {
		env[k] = v
	}
	for k, v := range integEnv {
		env[k] = v
	}
	// Marker for scripts and dotfiles to detect they are inside a bothy
	// (and which one), like TOOLBOX_PATH / DISTROBOX_ENTER_PATH in the
	// neighbouring tools. Reserved: set last so config env cannot mask it.
	env["BOTHY"] = name

	var network string
	switch cfg.Network {
	case config.NetworkHost:
		network = "host"
	case config.NetworkNone:
		network = "none"
	case config.NetworkPrivate:
		network = "" // podman's rootless default (pasta)
	}

	entrypoint := []string{
		BinaryMountPath, "init",
		"--uid", strconv.Itoa(u.UID),
		"--gid", strconv.Itoa(u.GID),
		"--username", u.Username,
		"--shell", u.Shell,
		"--home", ContainerHome(u),
		"--stamp", stamp,
	}
	for _, pkg := range cfg.Packages {
		entrypoint = append(entrypoint, "--package", pkg)
	}

	spec := runtime.CreateSpec{
		Name:     ContainerName(name),
		Hostname: cfg.Hostname,
		Labels: map[string]string{
			LabelManaged:       "true",
			LabelName:          name,
			LabelSchemaVersion: strconv.Itoa(config.SupportedSchemaVersion),
			LabelHome:          cfg.Home,
			LabelConfigHash:    hash,
			LabelStamp:         stamp,
		},
		Userns: "keep-id",
		// Init runs as root to create the user; enter/run exec as the user.
		User: "root:root",
		// Without label=disable, SELinux denies the container access to
		// bind-mounted user files (toolbox and distrobox do the same).
		SecurityLabelDisable: true,
		UlimitHost:           true,
		Network:              network,
		Env:                  env,
		Mounts:               mounts,
		ExtraArgs:            cfg.ExtraPodmanArgs,
		Image:                cfg.Image,
		Entrypoint:           entrypoint,
	}
	return spec, warnings, nil
}
