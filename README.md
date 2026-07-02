# bothy

Isolated, declarative development environments on rootless podman.

A bothy is a small shelter in the hills: everything you need, nothing you
don't, and it doesn't touch the landscape around it. In the same spirit,
`bothy` gives you containerised dev environments like
[toolbox](https://github.com/containers/toolbox) or
[distrobox](https://github.com/89luca89/distrobox), with two differences:

- **Declarative manifests.** Each environment is one YAML file. Recreate it
  anywhere, diff it, review it, put it in git.
- **A hard isolation boundary.** Each bothy gets its own private home
  directory. Nothing from your real home crosses in unless the manifest
  declares it: individual files and directories as (default read-only)
  mounts, and host features (GUI, audio, D-Bus, SSH agent, ...) as explicit
  per-feature toggles. Toolbox and distrobox mount your whole `$HOME`;
  bothy never does.

That makes a bothy a decent place for the tools you don't want anywhere near
your real dotfiles and credentials: client work with its own `~/.azure`,
experiments with `curl | bash` installers, per-project language toolchains.
(It is an isolation *boundary*, not a security sandbox; see
[docs/DESIGN.md](docs/DESIGN.md) for the honest version.)

## Status

Walking skeleton. `create` / `enter` / `run` / `list` / `stop` / `rm` work
against rootless podman with private homes, read-only mounts, and network
modes. The `integration:` toggles are parsed and validated but not wired to
podman yet (enabling one prints a warning); their planned wiring is
documented in `internal/engine/integrations.go` and
[docs/DESIGN.md](docs/DESIGN.md).

## Requirements

- Linux, podman >= 4.0 (rootless)
- Run bothy on the host, not inside a toolbox/distrobox container. bothy
  refuses to drive a container-local podman because that corrupts the host's
  rootless podman state; from inside a toolbox you can opt in with
  `BOTHY_PODMAN="flatpak-spawn --host podman"`.

## Quickstart

```console
$ make build   # produces bin/bothy (static)
```

Write a manifest at `~/.config/bothy/work.yaml`:

```yaml
schema_version: 1
image: registry.fedoraproject.org/fedora:42

mounts:
  - source: ~/.config/nvim   # read-only by default; target defaults to the same path

env:
  EDITOR: nvim

packages:
  - git
  - neovim
```

Then:

```console
$ bothy create work
$ bothy enter work            # login shell, private home at ~/.local/share/bothy/homes/work
$ bothy run work -- git status
$ bothy list
$ bothy stop work
$ bothy rm work               # asks before deleting the home; --keep-home / --force
```

`bothy create scratch --image registry.fedoraproject.org/fedora:42` works
without a manifest for throwaway environments, and
`bothy config show <name>` prints the fully resolved configuration
(built-in defaults < `~/.config/bothy/config.yaml` `defaults:` < manifest <
flags).

If the global defaults or the manifest file change after a bothy was
created, `enter`/`run` print a drift warning suggesting a recreate. The
check covers the declarative file layers only; one-off `create` flags are
not declared state and never count as drift.

## Manifest reference (schema_version 1)

| Field | Default | Meaning |
| --- | --- | --- |
| `schema_version` | required | must be `1` |
| `image` | required | container image |
| `hostname` | the bothy name | container hostname |
| `home` | `~/.local/share/bothy/homes/<name>` | host path of the private home |
| `mounts` | `[]` | list of `{source, target, mode}`; `target` defaults to `source`, `mode` to `ro` (`rw` and `overlay` available) |
| `integration.gui/audio/dbus/devices/fonts/ssh_agent/timezone` | all `false` | host-feature toggles (stubbed) |
| `network` | `private` | `host`, `private`, or `none` |
| `env` | `{}` | environment variables |
| `packages` | `[]` | installed on first boot (dnf/apt-get/pacman, best-effort) |
| `extra_podman_args` | `[]` | escape hatch, appended to `podman create` |

Unknown keys are errors. Paths accept `~`.

Inside every bothy the reserved `BOTHY` environment variable holds the
bothy's name, so shared dotfiles can branch on it (for example, pointing
lazy.nvim's lockfile at a writable path when `~/.config/nvim` is mounted
read-only).

`mode: overlay` (or `--mount-overlay`) gives the bothy a *writable view* of
a host directory whose writes land in a copy-on-write layer under the bothy
home: the container thinks it is modifying your files, but the host source
is never touched, and the delta survives restarts (deleted with `bothy rm`,
kept with `--keep-home`). Mind the trade-off: the bothy can still read
everything under the source. Whole-home overlays are not possible (the
kernel refuses a lower layer containing other mounts, and rootless podman's
own storage lives under `~/.local/share/containers`); overlay the
directories you work in instead.

## Design

The architecture (toolbox-style init entrypoint, readiness stamps, config
hashing and drift detection, the researched host-integration matrix, and the
open questions) is written up in [docs/DESIGN.md](docs/DESIGN.md).

## License

Apache-2.0
