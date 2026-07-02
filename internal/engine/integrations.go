package engine

// Host-integration toggles. Every toggle in this file is currently a stub:
// it parses, validates, and participates in the config hash, but adds no
// mounts or environment, and create prints a warning when one is enabled.
//
// The TODO comments record the exact wiring each toggle will add. They are
// sourced from reading toolbox main @ 0851179 (src/cmd/create.go,
// src/cmd/initContainer.go, src/pkg/utils/utils.go) and distrobox 1.8.2.5
// (distrobox-create, distrobox-init, distrobox-enter); the full comparison
// lives in docs/DESIGN.md under "Host integration". Unlike both tools,
// bothy mounts individual sockets rather than the whole $XDG_RUNTIME_DIR,
// so nothing undeclared leaks in.

import (
	"fmt"

	"github.com/jaydoubleu/bothy/internal/config"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

// integrationMounts collects the mounts and environment for every enabled
// integration toggle, plus a warning per toggle that is still a stub.
func integrationMounts(cfg *config.Config) ([]runtime.Mount, map[string]string, []string) {
	var mounts []runtime.Mount
	env := map[string]string{}
	var warnings []string

	add := func(name string, enabled bool, wiring func() ([]runtime.Mount, map[string]string)) {
		if !enabled {
			return
		}
		m, e := wiring()
		if m == nil && len(e) == 0 {
			warnings = append(warnings, fmt.Sprintf("integration.%s is enabled but not implemented yet; it currently has no effect", name))
			return
		}
		mounts = append(mounts, m...)
		for k, v := range e {
			env[k] = v
		}
	}

	add("gui", cfg.Integration.GUI, guiWiring)
	add("audio", cfg.Integration.Audio, audioWiring)
	add("dbus", cfg.Integration.DBus, dbusWiring)
	add("devices", cfg.Integration.Devices, devicesWiring)
	add("fonts", cfg.Integration.Fonts, fontsWiring)
	add("ssh_agent", cfg.Integration.SSHAgent, sshAgentWiring)
	add("timezone", cfg.Integration.Timezone, timezoneWiring)

	return mounts, env, warnings
}

// guiWiring will wire Wayland and X11.
//
// Toolbox and distrobox both mount the entire $XDG_RUNTIME_DIR (distrobox
// hardcodes /run/user/<uid>) and all of /tmp (which covers /tmp/.X11-unix),
// then forward WAYLAND_DISPLAY, DISPLAY and XAUTHORITY at exec time.
// Neither copies or regenerates xauth cookies; --userns=keep-id makes the
// forwarded XAUTHORITY file readable as-is.
//
// TODO(gui): add, at create time:
//   - mount $XDG_RUNTIME_DIR/$WAYLAND_DISPLAY at the same in-container path
//     (default wayland-0 when WAYLAND_DISPLAY is unset)
//   - mount /tmp/.X11-unix at /tmp/.X11-unix
//   - mount the file $XAUTHORITY points at, read-only, when set
//
// and at exec time forward WAYLAND_DISPLAY, DISPLAY, XAUTHORITY and set
// XDG_RUNTIME_DIR. Per-socket mounting misses side sockets and sockets
// created after create; the whole-runtime-dir fallback is an open question
// in docs/DESIGN.md.
func guiWiring() ([]runtime.Mount, map[string]string) {
	return nil, nil
}

// audioWiring will wire PipeWire and PulseAudio.
//
// Neither toolbox nor distrobox handles audio explicitly; it works for them
// only because the whole $XDG_RUNTIME_DIR is mounted. Neither sets
// PULSE_SERVER.
//
// TODO(audio): mount $XDG_RUNTIME_DIR/pipewire-0,
// $XDG_RUNTIME_DIR/pipewire-0-manager and $XDG_RUNTIME_DIR/pulse/native at
// the same in-container paths, and set
// PULSE_SERVER=unix:$XDG_RUNTIME_DIR/pulse/native.
func audioWiring() ([]runtime.Mount, map[string]string) {
	return nil, nil
}

// dbusWiring will wire the D-Bus session bus.
//
// In both tools the session bus socket rides the runtime-dir mount and
// DBUS_SESSION_BUS_ADDRESS is forwarded. For the system bus they diverge:
// toolbox mounts /run/dbus/system_bus_socket read-write, distrobox
// deliberately excludes it (package managers misbehave when they see the
// host system bus). Bothy sides with distrobox: system bus off by default,
// possibly a separate sub-toggle later.
//
// TODO(dbus): mount $XDG_RUNTIME_DIR/bus at the same in-container path and
// set DBUS_SESSION_BUS_ADDRESS=unix:path=$XDG_RUNTIME_DIR/bus.
func dbusWiring() ([]runtime.Mount, map[string]string) {
	return nil, nil
}

// devicesWiring will wire GPU (and later other) device nodes.
//
// Toolbox and distrobox mount ALL of /dev (rslave) under --privileged;
// neither uses --device at all. Bothy deliberately diverges: no
// --privileged, only named devices.
//
// TODO(devices): pass --device /dev/dri (needs a Devices field on
// runtime.CreateSpec); document /dev/snd and /dev/kvm as future granular
// options.
func devicesWiring() ([]runtime.Mount, map[string]string) {
	return nil, nil
}

// fontsWiring will expose host fonts.
//
// Toolbox mounts nothing for fonts (it relies on the image plus the $HOME
// mount). Distrobox binds host /usr/share/fonts, /usr/share/themes and
// /usr/share/icons to /usr/local/share/{fonts,themes,icons} and forces
// /usr/local/share into XDG_DATA_DIRS.
//
// TODO(fonts): follow distrobox: mount host /usr/share/fonts read-only at
// /usr/local/share/fonts and the real host ~/.local/share/fonts read-only
// at the same path under the container home; ensure /usr/local/share is in
// XDG_DATA_DIRS. Fontconfig rebuilds its cache into the bothy's private
// ~/.cache/fontconfig on first use (slow first GUI start; see
// docs/DESIGN.md).
func fontsWiring() ([]runtime.Mount, map[string]string) {
	return nil, nil
}

// sshAgentWiring will expose the host SSH agent.
//
// Neither tool mounts the agent socket specifically; SSH_AUTH_SOCK is
// forwarded and happens to resolve through the runtime-dir//tmp mounts.
// Bothy has no such blanket mounts, and the agent socket can live under an
// arbitrary directory, so it gets a fixed in-container path instead.
//
// TODO(ssh_agent): mount the socket $SSH_AUTH_SOCK resolves to at
// /run/bothy/ssh-agent.sock and set SSH_AUTH_SOCK=/run/bothy/ssh-agent.sock
// at exec time.
func sshAgentWiring() ([]runtime.Mount, map[string]string) {
	return nil, nil
}

// timezoneWiring will expose the host timezone.
//
// Toolbox symlinks /etc/localtime to /run/host/etc/localtime and writes
// /etc/timezone from init; distrobox read-only binds /etc/localtime with a
// copy fallback. Neither handles TZ.
//
// TODO(timezone): mount /etc/localtime read-only at /etc/localtime
// (resolving the host symlink first). The mount is fixed at create time, so
// a host timezone change needs a recreate; documented in docs/DESIGN.md.
func timezoneWiring() ([]runtime.Mount, map[string]string) {
	return nil, nil
}
