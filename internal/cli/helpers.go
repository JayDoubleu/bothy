package cli

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jaydoubleu/bothy/internal/config"
	"github.com/jaydoubleu/bothy/internal/engine"
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func validateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid bothy name %q (letters, digits, '_', '.', '-'; must start with a letter or digit)", name)
	}
	return nil
}

func hostUser() (engine.User, error) {
	u, err := user.Current()
	if err != nil {
		return engine.User{}, fmt.Errorf("cannot determine current user: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return engine.User{}, fmt.Errorf("non-numeric UID %q", u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return engine.User{}, fmt.Errorf("non-numeric GID %q", u.Gid)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return engine.User{Username: u.Username, UID: uid, GID: gid, Shell: shell}, nil
}

// overrideFlags are the CLI-level config overrides; they form the highest
// precedence manifest layer.
type overrideFlags struct {
	image        string
	hostname     string
	home         string
	network      string
	noNetwork    bool
	mountRO      []string
	mountRW      []string
	mountOverlay []string
	env          []string
	gui          bool
	audio        bool
	dbus         bool
	devices      bool
	fonts        bool
	sshAgent     bool
	timezone     bool
}

func (f *overrideFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&f.image, "image", "", "container image (overrides the manifest)")
	flags.StringVar(&f.hostname, "hostname", "", "container hostname (default: the bothy name)")
	flags.StringVar(&f.home, "home", "", "host path for the bothy's home directory")
	flags.StringVar(&f.network, "network", "", "network mode: host, private or none (default private)")
	flags.BoolVar(&f.noNetwork, "no-network", false, "shorthand for --network none")
	flags.StringArrayVar(&f.mountRO, "mount-ro", nil, "read-only mount src[:dst] (repeatable)")
	flags.StringArrayVar(&f.mountRW, "mount-rw", nil, "read-write mount src[:dst] (repeatable)")
	flags.StringArrayVar(&f.mountOverlay, "mount-overlay", nil, "copy-on-write mount src[:dst]: writable inside, host source untouched (repeatable)")
	flags.StringArrayVar(&f.env, "env", nil, "environment variable KEY=VALUE (repeatable)")
	flags.BoolVar(&f.gui, "gui", false, "enable the gui integration toggle")
	flags.BoolVar(&f.audio, "audio", false, "enable the audio integration toggle")
	flags.BoolVar(&f.dbus, "dbus", false, "enable the dbus integration toggle")
	flags.BoolVar(&f.devices, "devices", false, "enable the devices integration toggle")
	flags.BoolVar(&f.fonts, "fonts", false, "enable the fonts integration toggle")
	flags.BoolVar(&f.sshAgent, "ssh-agent", false, "enable the ssh_agent integration toggle")
	flags.BoolVar(&f.timezone, "timezone", false, "enable the timezone integration toggle")
}

// manifest converts the flags into a manifest-shaped overlay.
func (f *overrideFlags) manifest() (*config.Manifest, error) {
	m := &config.Manifest{
		Image:    f.image,
		Hostname: f.hostname,
		Home:     f.home,
		Network:  f.network,
	}
	if f.noNetwork {
		if f.network != "" && f.network != config.NetworkNone {
			return nil, errors.New("--no-network conflicts with --network " + f.network)
		}
		m.Network = config.NetworkNone
	}
	for _, s := range f.mountRO {
		m.Mounts = append(m.Mounts, parseMountFlag(s, config.ModeRO))
	}
	for _, s := range f.mountRW {
		m.Mounts = append(m.Mounts, parseMountFlag(s, config.ModeRW))
	}
	for _, s := range f.mountOverlay {
		m.Mounts = append(m.Mounts, parseMountFlag(s, config.ModeOverlay))
	}
	for _, e := range f.env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --env %q (expected KEY=VALUE)", e)
		}
		if m.Env == nil {
			m.Env = map[string]string{}
		}
		m.Env[k] = v
	}
	setIf := func(dst **bool, v bool) {
		if v {
			t := true
			*dst = &t
		}
	}
	setIf(&m.Integration.GUI, f.gui)
	setIf(&m.Integration.Audio, f.audio)
	setIf(&m.Integration.DBus, f.dbus)
	setIf(&m.Integration.Devices, f.devices)
	setIf(&m.Integration.Fonts, f.fonts)
	setIf(&m.Integration.SSHAgent, f.sshAgent)
	setIf(&m.Integration.Timezone, f.timezone)
	return m, nil
}

func parseMountFlag(s, mode string) config.Mount {
	src, dst, _ := strings.Cut(s, ":")
	return config.Mount{Source: src, Target: dst, Mode: mode}
}

// resolveConfig loads the global defaults plus the manifest for name (from
// file if given, otherwise the default path) and merges the overlay on top.
// requireManifest controls whether a missing default-path manifest is an
// error or simply an absent layer (create allows --image-only usage).
func resolveConfig(name, file string, overlay *config.Manifest, requireManifest bool) (*config.Config, error) {
	globalPath, err := config.GlobalConfigPath()
	if err != nil {
		return nil, err
	}
	global, err := config.LoadGlobal(globalPath)
	if err != nil {
		return nil, err
	}

	manifestPath := file
	if manifestPath == "" {
		manifestPath, err = config.DefaultManifestPath(name)
		if err != nil {
			return nil, err
		}
	}
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		if file == "" && errors.Is(err, os.ErrNotExist) {
			if requireManifest {
				return nil, fmt.Errorf("no manifest found at %s (create one, or pass -f <file>)", manifestPath)
			}
			manifest = nil
		} else {
			return nil, err
		}
	}
	if manifest == nil && (overlay == nil || overlay.Image == "") {
		return nil, fmt.Errorf("no manifest found at %s (create one, pass -f <file>, or pass --image)", manifestPath)
	}

	u, err := hostUser()
	if err != nil {
		return nil, err
	}
	var defaults *config.Manifest
	if global != nil {
		defaults = global.Defaults
	}
	return config.Resolve(config.ResolveOptions{Name: name, Username: u.Username}, defaults, manifest, overlay)
}
