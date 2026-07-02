package config

import (
	"fmt"
	"path/filepath"
)

// ResolveOptions carries per-invocation context needed to turn manifest
// layers into a resolved Config.
type ResolveOptions struct {
	// Name is the bothy name; it seeds the default hostname and home path.
	Name string
	// Username is the host username; mount targets with a leading ~ expand
	// against the container home /home/<Username>.
	Username string
}

// Resolve merges manifest layers (lowest precedence first, nil layers
// skipped) over the built-in defaults and returns a validated, fully
// expanded Config.
//
// Merge semantics: scalars from later layers replace earlier ones; mounts,
// packages and extra_podman_args concatenate in layer order; env merges per
// key with later layers winning; integration toggles replace only when a
// layer sets them explicitly.
func Resolve(opts ResolveOptions, layers ...*Manifest) (*Config, error) {
	merged := &Manifest{}
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		applyLayer(merged, layer)
	}

	cfg := &Config{
		Image:           merged.Image,
		Hostname:        merged.Hostname,
		Home:            merged.Home,
		Network:         merged.Network,
		Env:             merged.Env,
		Packages:        merged.Packages,
		ExtraPodmanArgs: merged.ExtraPodmanArgs,
		Integration: ResolvedIntegration{
			GUI:      boolOf(merged.Integration.GUI),
			Audio:    boolOf(merged.Integration.Audio),
			DBus:     boolOf(merged.Integration.DBus),
			Devices:  boolOf(merged.Integration.Devices),
			Fonts:    boolOf(merged.Integration.Fonts),
			SSHAgent: boolOf(merged.Integration.SSHAgent),
			Timezone: boolOf(merged.Integration.Timezone),
		},
	}
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}
	if cfg.Packages == nil {
		cfg.Packages = []string{}
	}
	if cfg.ExtraPodmanArgs == nil {
		cfg.ExtraPodmanArgs = []string{}
	}

	// Built-in defaults.
	if cfg.Hostname == "" {
		cfg.Hostname = opts.Name
	}
	if cfg.Network == "" {
		cfg.Network = NetworkPrivate
	}
	if cfg.Home == "" {
		home, err := DefaultHomePath(opts.Name)
		if err != nil {
			return nil, err
		}
		cfg.Home = home
	}

	// Path expansion: host-side paths expand against the host home, mount
	// targets against the container home.
	home, err := ExpandHostPath(cfg.Home)
	if err != nil {
		return nil, err
	}
	cfg.Home = home

	containerHome := "/home/" + opts.Username
	cfg.Mounts = make([]ResolvedMount, 0, len(merged.Mounts))
	for i, m := range merged.Mounts {
		source, err := ExpandHostPath(m.Source)
		if err != nil {
			return nil, err
		}
		targetRaw := m.Target
		if targetRaw == "" {
			targetRaw = m.Source
		}
		target := expandAgainst(targetRaw, containerHome)
		mode := m.Mode
		if mode == "" {
			mode = "ro"
		}
		if !filepath.IsAbs(source) {
			return nil, fmt.Errorf("mounts[%d]: source %q must be an absolute path or start with ~", i, m.Source)
		}
		if !filepath.IsAbs(target) {
			return nil, fmt.Errorf("mounts[%d]: target %q must be an absolute path or start with ~", i, targetRaw)
		}
		cfg.Mounts = append(cfg.Mounts, ResolvedMount{Source: source, Target: target, Mode: mode})
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyLayer(dst, src *Manifest) {
	if src.Image != "" {
		dst.Image = src.Image
	}
	if src.Hostname != "" {
		dst.Hostname = src.Hostname
	}
	if src.Home != "" {
		dst.Home = src.Home
	}
	if src.Network != "" {
		dst.Network = src.Network
	}
	dst.Mounts = append(dst.Mounts, src.Mounts...)
	dst.Packages = append(dst.Packages, src.Packages...)
	dst.ExtraPodmanArgs = append(dst.ExtraPodmanArgs, src.ExtraPodmanArgs...)
	if len(src.Env) > 0 {
		if dst.Env == nil {
			dst.Env = map[string]string{}
		}
		for k, v := range src.Env {
			dst.Env[k] = v
		}
	}
	applyToggle(&dst.Integration.GUI, src.Integration.GUI)
	applyToggle(&dst.Integration.Audio, src.Integration.Audio)
	applyToggle(&dst.Integration.DBus, src.Integration.DBus)
	applyToggle(&dst.Integration.Devices, src.Integration.Devices)
	applyToggle(&dst.Integration.Fonts, src.Integration.Fonts)
	applyToggle(&dst.Integration.SSHAgent, src.Integration.SSHAgent)
	applyToggle(&dst.Integration.Timezone, src.Integration.Timezone)
}

func applyToggle(dst **bool, src *bool) {
	if src != nil {
		*dst = src
	}
}

func boolOf(p *bool) bool {
	return p != nil && *p
}

// validate checks the fully merged result.
func (c *Config) validate() error {
	if c.Image == "" {
		return fmt.Errorf("image is required (set \"image:\" in the manifest or pass --image)")
	}
	if err := validNetwork(c.Network, false); err != nil {
		return err
	}
	for i, m := range c.Mounts {
		if err := validMode(m.Mode, false); err != nil {
			return fmt.Errorf("mounts[%d]: %w", i, err)
		}
	}
	return nil
}
