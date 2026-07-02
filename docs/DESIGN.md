# bothy design

bothy is a command line tool for creating and managing containerised development
environments on Linux, built on rootless podman. It occupies the same space as
Fedora Toolbox and Distrobox, with two deliberate differences: every environment
is described by a declarative YAML manifest, and every environment gets a hard
isolation boundary by default. Nothing from the real host home directory crosses
into a bothy unless the manifest explicitly declares it.

## 1. Overview

### Goals

- **Declarative environments.** A bothy is defined by a manifest file. The
  manifest is the source of truth: image, mounts, host integrations, network
  mode, packages. Recreating an environment from its manifest is the normal
  workflow, not an afterthought.
- **Isolation by default, integration by declaration.** Toolbox and distrobox
  optimise for seamlessness: they mount the host home wholesale, run with
  `--privileged`, share the host PID and IPC namespaces, and expose the entire
  host filesystem at `/run/host`. bothy inverts this. Each bothy has its own
  private home directory on the host. Individual files or directories from the
  real home are shared only through explicit `mounts:` entries, defaulting to
  read-only. Host desktop plumbing (Wayland, audio, D-Bus, SSH agent, and so
  on) is shared only through explicit per-feature `integration:` toggles, all
  of which default to off.
- **Work on arbitrary base images.** Like toolbox and distrobox, bothy sets up
  a matching user account inside plain fedora, ubuntu, or arch images at first
  boot. No special bothy images are required.
- **Boring, inspectable mechanics.** bothy shells out to podman with arguments
  you could type yourself, and records everything it needs to manage a
  container in labels on that container. `podman inspect` tells the whole
  story.

### Non-goals

- **Sandboxing untrusted code.** The isolation boundary exists to keep
  environments tidy and to make sharing an explicit, auditable decision. It is
  not a security sandbox. A bothy runs as your user with a rootless user
  namespace and passwordless sudo inside the container; anything mounted read
  and write is fully exposed.
- **Replacing toolbox or distrobox for the "my host, but with other packages"
  use case.** If you want your whole home directory and desktop session inside
  the container, those tools already do it well.
- **Docker support in the first iteration.** The runtime layer is an interface
  so Docker can come later, but only podman is implemented now.
- **Orchestration, images builds, multi-container setups.** One manifest, one
  container.

## 2. Architecture

### Components

The module is `github.com/jaydoubleu/bothy`. The binary is a thin `main` in
`cmd/bothy` delegating to internal packages:

- `internal/cli`: cobra command tree. `create`, `enter`, `run`, `list`,
  `stop`, `rm`, `config show`, and a hidden `init` subcommand that only runs
  inside containers as the entrypoint. Flags translate into a config overlay
  that participates in the normal precedence merge.
- `internal/config`: manifest and global-config types, strict YAML loading,
  precedence merging, validation, path expansion. Produces a fully resolved
  config value.
- `internal/engine`: turns a resolved config into a `runtime.CreateSpec`
  (mounts, labels, user namespace flags, entrypoint, network), plus lifecycle
  helpers (readiness polling, drift detection, home directory management).
  `internal/engine/integrations.go` is the single place where integration
  toggles map to concrete mounts, env vars, and flags.
- `internal/runtime`: the `Runtime` interface and its implementations. The
  `Podman` implementation shells out via `os/exec`. A `Fake` implementation
  records calls and plays scripted responses for tests.

### Why the toolbox architecture

There are two proven architectures in this space, verified against source:
toolbox `main` at commit 0851179 (`src/cmd/create.go`,
`src/cmd/initContainer.go`, `src/pkg/utils/utils.go`) and distrobox 1.8.2.5
(`distrobox-create`, `distrobox-init`, `distrobox-enter`).

Toolbox bakes all mounts in at `podman create` time and injects environment at
`podman exec` time from a fixed allowlist of 43 variables. Its entrypoint is
the toolbox binary itself, bind-mounted into the container, running
`toolbox init-container`: it performs first-boot setup (user account, sudoers,
symlinks), touches a readiness stamp file in a shared directory that
`toolbox run` polls with a 25 second timeout, then blocks forever.

Distrobox mounts the entire host root filesystem at `/run/host` and does most
of its wiring from inside the container: `distrobox-init` is a bind-mounted
shell script entrypoint that installs packages, creates the user, and binds
host paths from `/run/host` outward. It signals readiness by printing
`container_setup_done` to the container logs, then loops every 15 seconds
re-syncing `/etc/hosts`, `/etc/resolv.conf`, `/etc/localtime`, and
`/etc/hostname` from the host.

bothy follows the toolbox architecture: create-time mounts, its own binary
bind-mounted read-only as the init entrypoint, and a readiness stamp file that
the host polls. Create-time mounts keep the container spec fully declarative
and inspectable; the mounted-own-binary trick means no shell script injection
and no image requirements; the stamp file gives `enter` and `run` a reliable
readiness signal without scraping logs.

What bothy deliberately does **not** adopt from either tool:

- No `/:/run/host` mount. The host filesystem is not visible inside a bothy.
- No `--privileged`.
- No `--pid host`, no `--ipc host`.
- No wholesale `$HOME` mount. The private home strategy in section 4 replaces
  it.

Selective, declared sharing is the entire point of the tool, and each of those
mechanisms is a wholesale bypass of it.

## 3. Configuration model

### Files and precedence

A manifest lives at `~/.config/bothy/<name>.yaml`, or anywhere via
`bothy create <name> -f path/to/manifest.yaml`. A global defaults file at
`~/.config/bothy/config.yaml` may contain a `defaults:` block with the same
shape as a manifest, every field optional.

Resolution order, lowest to highest precedence:

1. Built-in defaults
2. Global `defaults:` block
3. Manifest
4. CLI flags

Merge semantics per field kind:

- **Scalars** (`image`, `hostname`, `home`, `network`, each `integration.*`
  boolean): later layers replace earlier ones.
- **Lists** (`mounts`, `packages`, `extra_podman_args`): concatenated,
  defaults first, then manifest, then flags. Global default mounts always
  apply and manifests add to them.
- **Maps** (`env`): merged per key, later layers winning on conflicts.

`bothy config show <name>` prints the fully resolved config as YAML. It
accepts the same override flags as `create`, so it previews exactly what a
create with those flags would resolve. (Drift detection hashes the
declarative file layers, not this resolved value; see section 5.)

### Schema

Parsing is strict: `yaml.v3` with `KnownFields(true)`, so unknown keys are
errors, not silent no-ops. Validation errors are one-line and human friendly,
with file and field context (`work.yaml: mounts[1].mode: "wr" is not valid,
expected "ro" or "rw"`), never stack traces.

Annotated schema, version 1:

```yaml
# Required. The only accepted value is 1. Bump means breaking schema change.
schema_version: 1

# Required. Any image reference podman can pull.
image: registry.fedoraproject.org/fedora:42

# Optional. Container hostname. Defaults to the bothy name.
hostname: work

# Optional. Host path for this bothy's private home directory.
# Defaults to ~/.local/share/bothy/homes/<name>. See section 4.
home: ~/bothy-homes/work

# Optional. Explicit shares from the host. Each entry:
#   source: host path (required). ~ expands against the real host home.
#   target: container path. Defaults to the source path. ~ expands against
#           the container home, /home/<username>.
#   mode:   "ro" (default), "rw", or "overlay". Overlay mounts the source as
#           a read-only overlayfs lower layer: the bothy sees a writable
#           directory, but writes land in a copy-on-write upper layer under
#           the bothy home and the host source is never modified.
mounts:
  - source: ~/.config/nvim        # read-only by default
  - source: ~/projects/foo
    target: ~/foo                 # lands at /home/<user>/foo
    mode: rw
  - source: ~/devel
    mode: overlay                 # writable inside, host untouched

# Optional. Host integration toggles. All default to false: a manifest that
# omits this block gets no host desktop plumbing at all.
integration:
  gui: false          # Wayland/X11 sockets + display env
  audio: false        # PipeWire/PulseAudio sockets + PULSE_SERVER
  dbus: false         # session bus socket + DBUS_SESSION_BUS_ADDRESS
  devices: false      # /dev/dri via --device (not all of /dev)
  fonts: false        # host fonts read-only at fontconfig-visible paths
  ssh_agent: false    # SSH_AUTH_SOCK socket at a fixed container path
  timezone: false     # /etc/localtime read-only

# Optional. "host", "private" (default), or "none".
#   host:    --network host, container shares the host network namespace
#   private: podman's default rootless network (pasta)
#   none:    --network none, no network at all
network: private

# Optional. Extra environment variables set on every exec.
env:
  EDITOR: nvim

# Optional. Packages installed at first init via the image's detected package
# manager (dnf, apt-get, or pacman). Best effort: failures are logged, not
# fatal, and never block the container from becoming ready.
packages:
  - git
  - ripgrep

# Optional escape hatch. Appended verbatim to the podman create invocation,
# after everything bothy generates. If you need this often, file an issue.
extra_podman_args: []
```

Path expansion rules: `~` in host-side fields (`home`, `mounts[].source`)
expands against the real host home. `~` in `mounts[].target` expands against
the container home, `/home/<username>`, because that is where the path will
resolve when used. Paths that end up in podman `--volume` arguments (`home`,
mount sources and targets) must not contain `:` or `,`: podman's volume
syntax uses them as separators with no escaping, so validation rejects such
paths with a clear error instead of producing a garbled mount.

### Overlay mounts (copy-on-write)

`mode: overlay` uses podman's overlay volume option (`-v src:dst:O`) with
explicit `upperdir`/`workdir` so the delta persists. The upper and work
directories live under the bothy home at
`.local/state/bothy/overlays/<target-slug>-<digest>/`, where the slug is
the target path with slashes flattened and the digest is the first 8 hex
chars of sha256(target). The digest keeps the key injective (the readable
slug alone would collide for targets like `~/a-b` and `~/a/b`) while
remaining a pure function of the target path, so a reordered manifest
keeps its deltas. The directories share the home's lifecycle:
`rm` deletes the delta, `rm --keep-home` preserves it, and it survives
container stop/start. `create` prints a warning whenever overlay mounts are
present, because the trade-off inverts bothy's usual posture: the bothy can
READ everything under the source (credentials included); only writes are
contained.

Two kernel constraints apply (both verified against rootless podman 5.8.2).
First, overlayfs refuses a lower layer that contains other active mounts
("failed to clone lowerpath"), and rootless podman's own storage is mounted
under `~/.local/share/containers` whenever a container runs, so a whole-home
overlay is impossible with default storage; overlay the directories you work
in instead. Second, the upper dir must live outside the source tree, so a
source that contains the bothy home itself (such as `~/.local`) cannot work.
`create` detects podman's "Invalid argument" in the presence of overlay
mounts and prints a hint covering both. Also inherent to overlayfs:
host-side writes to the source while the overlay is mounted are formally
undefined; fine for code and dotfiles, risky for hot directories like
browser profiles.

### Example manifests

Headless minimum (`examples/minimal.yaml`):

```yaml
schema_version: 1
image: registry.fedoraproject.org/fedora:42
```

A work environment with a private Azure credential store (it simply lives in
the bothy's own home, never touching the real one), a shared editor config,
and GUI apps enabled (`examples/work.yaml`):

```yaml
schema_version: 1
image: registry.fedoraproject.org/fedora:42
mounts:
  - source: ~/.config/nvim
integration:
  gui: true
packages:
  - azure-cli
  - neovim
```

## 4. Home directory strategy

Every bothy gets a private home directory on the host, by default
`~/.local/share/bothy/homes/<name>`, created with mode 0700 at `create` time.
The `home:` manifest field overrides the location. This directory is
bind-mounted read and write at `/home/<username>` inside the container.

`--userns=keep-id` maps the host user to the same UID inside the rootless user
namespace, so files created inside the container are owned by the real user on
the host with no chown dance. On every `exec`, bothy sets `HOME`, `USER`, and
`SHELL` explicitly rather than trusting the image's passwd defaults.

The real host home is never mounted. There is no code path that mounts it;
sharing anything from it requires a `mounts:` entry naming the specific file or
directory. Declared mounts may target paths inside the bothy home, for example
a read-only nvim config at `~/.config/nvim`, and the mount shadows whatever the
bothy home has at that path.

The host home path is recorded in the
`com.github.jaydoubleu.bothy.home` label on the container, so `bothy rm` knows
what to delete without consulting any state file. Because deleting a home
directory is destructive, `rm` asks for interactive confirmation before doing
it; `--force`/`-y` skips the prompt and `--keep-home` skips the deletion.

Consequences worth stating:

- State in a bothy home survives `stop` and container recreation, as long as
  `rm --keep-home` is used or the same `home:` path is reused.
- Dotfiles do not follow you in automatically. That is the feature. Declare
  the ones you want.
- Caches (`~/.cache`) are per-bothy, which is usually what you want and has
  one known cost (fontconfig, see section 8).

## 5. Container lifecycle

### Create once, exec on enter

bothy uses the same lifecycle model as toolbox: a container is created once
with everything baked in, and `enter`/`run` are `podman exec` into the running
(or restarted) container. Containers are named `bothy-<name>` purely to avoid
name clashes; labels, not names, are the source of truth for management:

| Label | Value |
| --- | --- |
| `com.github.jaydoubleu.bothy.managed` | `true` |
| `com.github.jaydoubleu.bothy.name` | the bothy name |
| `com.github.jaydoubleu.bothy.schema-version` | `1` |
| `com.github.jaydoubleu.bothy.home` | host path of the bothy home |
| `com.github.jaydoubleu.bothy.config-hash` | sha256 of the declarative config layers (see below) |
| `com.github.jaydoubleu.bothy.manifest` | manifest path used at create time, empty for `--image-only` creates |
| `com.github.jaydoubleu.bothy.stamp` | readiness-stamp nonce (see init, step 5) |

The config hash covers the DECLARATIVE layers only: the global `defaults:`
block and the manifest file, each serialized canonically as JSON (fixed
field order, sorted map keys) and hashed in layer order. One-off CLI flags
are create-time inputs, not declared state, and are excluded on both sides
of the comparison; by construction, creating with `--gui` and entering
without it is not drift. The manifest label records which file to reload
when checking, so bothies created via `-f` anywhere are covered, not just
those with manifests at the default path.

### create

1. Load and merge config (section 3), validate, expand paths.
2. Preflight podman (below).
3. Create the bothy home directory (0700) if missing.
4. Build the `podman create` invocation:
   `--name bothy-<name>`, labels, `--userns=keep-id`,
   `--security-opt label=disable`, `--ulimit host`, `--user root:root`,
   the home mount, all declared mounts, integration mounts (once implemented),
   the network flag, `--hostname`, env, the init entrypoint as
   `--entrypoint '["/usr/libexec/bothy","init",...]'` (JSON-array exec
   form, so an image-defined ENTRYPOINT like postgres's is replaced rather
   than swallowing the init vector as arguments), then `extra_podman_args`,
   then the image.
5. `podman create`, `podman start`, then poll for init readiness. The poll
   also checks container liveness every couple of seconds, so an image
   where init cannot work (alpine without useradd, say) fails in seconds
   with "container exited during initialization" instead of burning the
   whole timeout.

Every bothy also gets a reserved `BOTHY=<name>` environment variable, set at
create time and again at exec time, so scripts and dotfiles shared into a
bothy can detect where they are running (the same job TOOLBOX_PATH and the
DISTROBOX_* variables do in the neighbouring tools). A practical example: a
read-only `~/.config/nvim` mount means lazy.nvim cannot write its
`lazy-lock.json` there, so an init.lua can branch on `vim.env.BOTHY` and
relocate the lockfile into the writable data dir. `BOTHY` is set after the
manifest's `env:` is merged, so config cannot mask it.

Two flags deserve explanation. `--security-opt label=disable` matches toolbox
and distrobox: on SELinux systems, container processes would otherwise be
denied access to bind-mounted user files unless every source were relabelled,
which is not acceptable for paths like `~/.config/nvim`. `--ulimit host`
matches toolbox and keeps resource limits identical to the host session, since
a bothy is a dev environment rather than a service workload.

### The init entrypoint

bothy mounts its own binary, found via `os.Executable()`, read-only at
`/usr/libexec/bothy`, and uses `bothy init` as the container entrypoint. This
is the toolbox trick and it removes any dependency on the image contents. The
container is created with `--user root:root` so init has the privileges to set
up the system; `enter`/`run` exec as the real user.

`bothy init`, running as PID 1 in the container:

1. **Ensure the user exists** with the host's username, UID, and GID. If
   the image ships a user squatting on the UID under another name (ubuntu's
   `ubuntu` at 1000), rename it first with `usermod --login`,
   toolbox/distrobox style. Then `useradd --home-dir /home/<user>
   --no-create-home --password '' --shell <shell> --uid <uid>`, falling
   back to `usermod` if a user with that name already exists in the image.
   If the host's shell does not exist in the image, fall back to
   `/bin/bash`, then `/bin/sh`. `--no-create-home` because the home is a
   mount that already exists.
2. **Grant sudo**: a sudoers drop-in with `NOPASSWD:ALL` (distrobox style),
   which is the honest posture for a rootless dev container where root inside
   the container is not a privilege boundary anyway.
3. **Best-effort locale setup** (planned; not in the walking skeleton yet).
4. **Install `packages:`** using whichever of dnf, apt-get, or pacman the
   image has, but only on the container's first boot: a marker at
   `/var/lib/bothy/init-done` in the container's writable layer (which
   survives stop/start but dies with the container) skips the install on
   restarts. Failures are logged and do not abort init: an unreachable
   mirror should not brick the environment.
5. **Write the readiness stamp** at
   `<bothy home>/.cache/bothy/ready-<nonce>`, where the nonce is a random
   value generated by `create`, passed to init via `--stamp`, and recorded in
   the `.stamp` label so `enter`/`run` know which file to poll. A nonce is
   used rather than the container ID because rootless podman only writes the
   `id=` field into `/run/.containerenv` for privileged containers, so init
   cannot learn its own ID (verified empirically; toolbox gets away with it
   because it runs `--privileged`). Because the bothy home is a bind mount,
   the stamp is visible on the host, unlike toolbox's stamp, which lives in
   the mounted runtime dir bothy does not mount by default. The nonce suffix
   also prevents a stale stamp from a previous container (same home kept via
   `rm --keep-home`, recreated container) from reading as ready.
6. **Block forever**, handling SIGTERM and SIGINT so `podman stop` terminates
   promptly instead of waiting for the kill timeout.

### enter and run

Both commands share one path: `podman start` if the container is not running
(removing the previous boot's stale readiness stamp first, so readiness gates
on the new boot's init actually finishing), poll the readiness stamp on the
host side (with a timeout, a liveness check that fails fast when the
container has exited, and a useful error pointing at `podman logs`), then
`podman exec --user <user> -it` with `HOME`, `USER`, `SHELL`, and configured
`env` set. A real TTY check (`x/term.IsTerminal`) decides whether to allocate
a container TTY, so redirected stdio (`bothy run x -- cat < /dev/null`) works
instead of hanging on a half-attached terminal.

`enter` resolves the user's login shell inside the container via
`getent passwd` and execs it as a login shell. `run` execs the given argv and
propagates the command's exit code as bothy's own exit code.

Before exec, both commands recompute the hash of the declarative config
layers (global `defaults:` plus the manifest file recorded in the manifest
label; see the label table) and compare it against the `config-hash` label.
The check is best-effort and silently skipped when there is nothing to
compare: an `--image-only` create declared no manifest, and a manifest file
that has since been moved or deleted cannot be reloaded. On mismatch they
print a clear warning:

```
warning: the configuration of "work" has changed since this container was created;
         run "bothy rm work && bothy create work" to apply it
```

and continue into the old container. The walking skeleton never auto-recreates;
a future `bothy recreate` (section 8) would make this one command.

### list, stop, rm

- `list`: `podman ps -a --filter label=com.github.jaydoubleu.bothy.managed=true
  --format json`, rendered as a NAME / IMAGE / STATUS table.
- `stop`: `podman stop bothy-<name>`.
- `rm`: `podman rm -f bothy-<name>`, then delete the bothy home directory read
  from the `.home` label. Deletion prompts interactively; `--keep-home`
  preserves the home, `--force`/`-y` skips the prompt.

Because labels, not names, are the source of truth, every command that
operates on an existing container (`enter`, `run`, `stop`, `rm`) first
checks the `managed=true` label and refuses to touch a container that
merely squats on the `bothy-` name prefix; `create` likewise explains that
the name is taken rather than advising `bothy rm` against a container it
does not manage.

### Podman preflight

Every command that shells out first runs `podman version --format json`. A
missing binary or a major version below 4 produces a single clear error:

```
bothy requires podman >= 4.0 for rootless --userns=keep-id support; found 3.4.2
```

Preflight also refuses to run when bothy itself is inside a container (it
checks for `/run/.containerenv` and `/.dockerenv`). Containers like toolbox
share `$HOME` and `$XDG_RUNTIME_DIR` with the host, so a podman binary found
inside one operates on the host podman's storage and pause-process state from
the wrong namespaces; in practice this corrupts the host's rootless podman
and can kill every rootless container on the host, including the one bothy is
running in. The `BOTHY_PODMAN` environment variable overrides the podman
command (split on whitespace, so `BOTHY_PODMAN="flatpak-spawn --host podman"`
works from a toolbox) and doubles as the explicit opt-in that skips this
guard.

## 6. Host integration

This section is the researched matrix behind the `integration:` toggles. Facts
about toolbox are verified against `main` at commit 0851179
(`src/cmd/create.go`, `src/cmd/initContainer.go`, `src/pkg/utils/utils.go`);
facts about distrobox against 1.8.2.5 (`distrobox-create`, `distrobox-init`,
`distrobox-enter`).

**Status in the walking skeleton:** every toggle is parsed, validated, merged,
and hashed, but only `network` is wired to podman. Enabling any other toggle
prints a warning that it is not yet implemented. The exact planned wiring for
each toggle lives as doc comments with TODOs in
`internal/engine/integrations.go`, mirroring the subsections below, so the
implementation work is mechanical.

A key background fact shapes everything here: neither toolbox nor distrobox
does per-feature socket plumbing. Both mount the entire `$XDG_RUNTIME_DIR`
(distrobox hardcodes `/run/user/<uid>`) and all of `/tmp`, and most desktop
integration simply falls out of that plus forwarded environment variables.
bothy refuses the blanket mounts, so each toggle must name its sockets
explicitly. That is more precise and more fragile; the trade-offs are honest
open questions in section 8.

Summary matrix (bothy column is the planned wiring):

| Toggle | toolbox | distrobox | bothy plan |
| --- | --- | --- | --- |
| gui | whole `$XDG_RUNTIME_DIR` + `/tmp`; env at exec | same, `/run/user/<uid>` hardcoded | per-socket: Wayland socket, `/tmp/.X11-unix`, `XAUTHORITY` file ro; 3 env vars |
| audio | nothing explicit (rides runtime dir) | nothing explicit (rides runtime dir) | `pipewire-0`, `pipewire-0-manager`, `pulse/native` sockets; sets `PULSE_SERVER` |
| dbus | session via runtime dir; system bus mounted rw | session via runtime dir; system bus deliberately excluded | session socket + env; system bus off |
| devices | all of `/dev` (rslave, `--privileged`) | all of `/dev` (rslave, `--privileged`) | `--device /dev/dri` only |
| fonts | nothing (relies on `$HOME` mount) | host share dirs to `/usr/local/share/*` | distrobox style, ro |
| ssh_agent | env forwarded, socket rides blanket mounts | same | socket at fixed `/run/bothy/ssh-agent.sock` + env |
| timezone | symlink to `/run/host/etc/localtime` | ro bind with copy fallback | ro bind `/etc/localtime` |
| network | `--network host` + resolv.conf handling | `--network host` + resolv.conf resync | `host`/`private`/`none` enum, wired now |

### gui

*toolbox:* mounts the whole `$XDG_RUNTIME_DIR` and all of `/tmp` (which covers
`/tmp/.X11-unix`) at create, and forwards `WAYLAND_DISPLAY`, `DISPLAY`, and
`XAUTHORITY` at exec from its env allowlist. *distrobox:* the same shape, with
`/run/user/<uid>` hardcoded as the runtime dir path. Neither tool mounts
display sockets individually, and neither copies or regenerates X authority
cookies: because both run with keep-id UID mapping, the forwarded `XAUTHORITY`
path simply works when the file is reachable through the mounts.

*bothy plan:* per-socket mounts, same paths inside and out:
`$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY` (the Wayland socket), the `/tmp/.X11-unix`
directory, and the `XAUTHORITY` file read-only if set. Forward
`WAYLAND_DISPLAY`, `DISPLAY`, `XAUTHORITY` at exec. A whole-runtime-dir
fallback is documented as an open question because per-socket mounting misses
side sockets and anything created after `create` (section 8).

### audio

*toolbox and distrobox:* zero explicit audio handling in either codebase.
Audio works purely because the runtime dir mount happens to contain the
PipeWire and PulseAudio sockets. Neither sets `PULSE_SERVER`; it is not even
in toolbox's 43-variable allowlist.

*bothy plan:* mount `$XDG_RUNTIME_DIR/pipewire-0`,
`$XDG_RUNTIME_DIR/pipewire-0-manager`, and `$XDG_RUNTIME_DIR/pulse/native` at
the same paths, and set `PULSE_SERVER=unix:$XDG_RUNTIME_DIR/pulse/native` so
PulseAudio clients find the socket without a full runtime dir.

### dbus

*toolbox:* the session bus socket rides the runtime dir mount and
`DBUS_SESSION_BUS_ADDRESS` is forwarded. For the system bus, toolbox resolves
and mounts `/run/dbus/system_bus_socket` read-write at the same path.
*distrobox:* session bus likewise rides the runtime dir mount with the env var
forwarded, but the system bus is deliberately excluded, because package
managers and other software inside the container misbehave when they can see
the host system bus.

*bothy plan:* mount the session bus socket `$XDG_RUNTIME_DIR/bus` at the same
path and forward `DBUS_SESSION_BUS_ADDRESS`. Side with distrobox on the system
bus: off by default, with a possible `dbus_system` sub-toggle later
(section 8).

### devices

*toolbox and distrobox:* both run `--privileged` and mount all of `/dev` with
rslave propagation; neither codebase contains a single `--device` flag.

*bothy plan:* the opposite: no `--privileged`, and `--device /dev/dri` for GPU
access, which is what GUI apps and hardware video decoding actually need.
`/dev/snd` (ALSA) and `/dev/kvm` are documented future candidates for granular
sub-options rather than silently widening this toggle.

### fonts

*toolbox:* mounts nothing for fonts; host fonts appear because the whole
`$HOME` is mounted and images bring their own system fonts. *distrobox:* binds
host `/usr/share/fonts`, `/usr/share/themes`, and `/usr/share/icons` to
`/usr/local/share/fonts`, `/usr/local/share/themes`, `/usr/local/share/icons`
and forces `/usr/local/share` into `XDG_DATA_DIRS`, so fontconfig and toolkits
find them without touching the image's own paths.

*bothy plan:* follow distrobox: host `/usr/share/fonts` read-only at
`/usr/local/share/fonts` (themes and icons as later additions), ensure
`/usr/local/share` is in `XDG_DATA_DIRS`, plus optionally the real home's
`~/.local/share/fonts` read-only. Fontconfig cache behaviour is a documented
gotcha (section 8), though less severe for bothy than for the others because a
private home means a private, persistent `~/.cache/fontconfig`.

### ssh_agent

*toolbox and distrobox:* no socket-specific handling in either; both forward
`SSH_AUTH_SOCK` and the socket happens to be reachable through the runtime dir
or `/tmp` blanket mounts.

*bothy plan:* without blanket mounts, same-path mounting is fragile: the agent
socket may live anywhere (gpg-agent and forwarded agents often sit under paths
bothy does not mount). So bothy resolves `$SSH_AUTH_SOCK` at create time,
mounts that socket at the fixed path `/run/bothy/ssh-agent.sock`, and sets
`SSH_AUTH_SOCK=/run/bothy/ssh-agent.sock` at exec.

### timezone

*toolbox:* symlinks `/etc/localtime` to `/run/host/etc/localtime` from init and
writes `/etc/timezone`. *distrobox:* read-only binds `/etc/localtime` with a
copy fallback when the bind is not possible. Neither handles `TZ`.

*bothy plan:* read-only bind `/etc/localtime` at create. Since the target of
the host symlink is resolved at create time, a host timezone change after
create is not reflected until recreate; accepted and documented.

### network (top-level, wired in the skeleton)

*toolbox:* `--network host` with `--dns none --no-hosts` and symlinks pointing
`/etc/resolv.conf` and `/etc/hosts` into `/run/host`. *distrobox:*
`--network host` with read-only binds and the 15 second resync loop. Both
special-case systemd-resolved's absolute-symlink stub-resolv.conf.

*bothy:* `network` is a top-level enum rather than an integration toggle,
because "no network" and "host network" are workload decisions, not host
plumbing. Mapping, implemented now:

- `host`: `--network host`. No resolv.conf gymnastics are needed because bothy
  does not mount anything over `/etc`.
- `private` (default): podman's rootless default, pasta, which manages
  resolv.conf itself.
- `none`: `--network none`. A `--no-network` CLI flag maps to this.

## 7. Runtime abstraction

`internal/runtime` defines:

```go
type Runtime interface {
    Preflight(ctx context.Context) error
    Create(ctx context.Context, spec CreateSpec) (id string, err error)
    Start(ctx context.Context, container string) error
    Stop(ctx context.Context, container string) error
    Remove(ctx context.Context, container string, force bool) error
    Exec(ctx context.Context, spec ExecSpec) error // non-zero exit as *ExitError
    List(ctx context.Context, labelFilters map[string]string) ([]Container, error)
    Inspect(ctx context.Context, container string) (*Container, error)
}
```

`CreateSpec` is a plain data struct (image, name, hostname, mounts, devices,
labels, env, user, network, entrypoint, extra args); `internal/engine` owns
the translation from resolved config to `CreateSpec`, and the runtime owns the
translation from `CreateSpec` to command-line arguments. This split keeps the
policy ("what a bothy is") testable without podman and the mechanism ("how
podman spells that") testable as pure argv construction.

- `Podman` shells out via `os/exec`. Non-interactive commands capture output;
  interactive `Exec` connects the user's stdio directly so TTYs, signals, and
  resize behave natively. `Preflight` implements the version check and the
  nested-container guard from section 5.
- `Fake` records every call and returns scripted responses. Engine and CLI
  tests run against it, asserting on the exact `CreateSpec`/argv produced for
  a known config.
- `Docker` is explicitly future work. The interface is shaped so a Docker
  implementation is additive, but no Docker code exists and none is planned
  for the skeleton.

## 8. Open questions

Honest list, roughly ordered by how likely each is to bite.

1. **Per-socket vs whole `$XDG_RUNTIME_DIR` mounting.** bothy chooses
   per-socket for isolation, against the practice of both reference tools.
   Known risks: side sockets (`pipewire-0-manager` is handled, but portals,
   `wayland-1`, and friends are not), sockets created after `create` (a
   compositor restart can change `$WAYLAND_DISPLAY`), and software that
   assumes a fully populated runtime dir. If per-socket proves too brittle, a
   `runtime_dir: true` escape toggle mounting the whole directory is the
   fallback; the default should stay per-socket.
2. **`XAUTHORITY` cookie handling.** The plan forwards the cookie file
   read-only and relies on keep-id UID mapping, exactly as toolbox and
   distrobox do (neither copies or regenerates cookies). Untested corners:
   cookies under paths that move between sessions, and X servers with
   host-based auth. Revisit only if real breakage shows up.
3. **Fontconfig cache.** No tool in this space manages it; the symptom is a
   slow first GUI app start while caches rebuild. bothy's private home means a
   private `~/.cache/fontconfig` that persists across enters, so the cost is
   paid once per bothy rather than per session. Documented, not solved.
4. **Create-time mounts freeze socket paths.** All integration mounts resolve
   at `create`. If `$WAYLAND_DISPLAY` or `$SSH_AUTH_SOCK` changes afterwards,
   the mounts are stale and the fix is recreate. This is inherent to the
   create-once model; the drift warning plus a future `recreate` command is
   the mitigation.
5. **pasta vs slirp4netns.** `private` relies on podman's default, which is
   pasta on anything recent and slirp4netns on older installs. bothy does not
   pin one; behaviour differences (port forwarding semantics, IPv6) are
   podman's to define. May need revisiting if `private` grows port-publishing
   options.
6. **runc vs crun.** Known differences relevant here: a runc bug with
   `/run/host`-style mounts (does not affect bothy, which has no such mount)
   and the crun-only `run.oci.keep_original_groups=1` annotation used to keep
   supplementary groups. bothy does not pin a runtime yet. Preferring crun via
   `--runtime=crun` when present is tempting but unimplemented; decide when a
   concrete need (like the groups annotation) arrives.
7. **`/run/host` escape hatch.** Should a `host_root: true` option exist for
   users who want distrobox-style full host visibility in one specific bothy?
   It cuts against the core premise, but as an explicit, per-manifest,
   loudly-documented opt-in it may be more honest than pushing users back to
   distrobox. Undecided.
8. **D-Bus system bus sub-toggle.** Off by default, siding with distrobox.
   Tools like `systemctl --host`-style workflows or firmware managers want it;
   a `dbus_system` sub-toggle mounting `/run/dbus/system_bus_socket` is the
   likely shape if demand shows up.
9. **`bothy recreate`.** The obvious answer to drift warnings:
   `rm --keep-home` plus `create` in one transaction, keeping the home and
   re-baking mounts. Needs care around confirming when the home path itself
   changed between configs. Not in the skeleton.
10. **Distrobox's keep-id probe.** Distrobox probes support for
    `--userns=keep-id:size=` with a throwaway container to handle differing
    podman versions. bothy currently assumes podman >= 4 and plain
    `keep-id`; if UID-range issues appear on exotic setups, adopt the probe.
11. **Overlay lower-layer mutation.** `mode: overlay` is implemented
    (section 3), but overlayfs leaves behaviour undefined when the host
    modifies a source directory while a bothy has it overlay-mounted, and in
    practice the host does exactly that for long-lived bothies. Options if
    it bites: document "stop the bothy before big host-side changes",
    snapshot the source at create time on reflink-capable filesystems, or
    detect staleness and suggest a restart. Waiting for real-world reports
    before choosing.
