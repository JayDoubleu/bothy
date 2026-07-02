package engine

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// InitOptions are the flags of the hidden "bothy init" subcommand, which
// runs as PID 1 (as root) inside every bothy container.
type InitOptions struct {
	UID      int
	GID      int
	Username string
	Shell    string
	Home     string
	Stamp    string
	Packages []string
}

// initDoneMarker lives in the container's writable layer, so it survives
// stop/start but dies with the container: package installs run only on the
// first boot of each container, not on every restart.
const initDoneMarker = "/var/lib/bothy/init-done"

// RunInit performs first-boot container setup and then blocks until the
// container is stopped. Everything except user creation is best-effort:
// failures are logged (visible via podman logs) but do not abort, so a
// missing package manager or sudo does not brick the environment.
func RunInit(opts InitOptions) error {
	log.SetFlags(0)
	log.SetPrefix("bothy-init: ")

	shell := resolveShell(opts.Shell)
	if err := ensureUser(opts, shell); err != nil {
		return err
	}
	ensureSudoers(opts.Username)
	if _, err := os.Stat(initDoneMarker); err == nil {
		log.Printf("packages: first boot already done; skipping install")
	} else {
		installPackages(opts.Packages)
		markInitDone()
	}
	if err := writeReadyStamp(opts); err != nil {
		return err
	}
	log.Printf("ready")

	waitForever()
	return nil
}

// markInitDone is best-effort: a read-only /var/lib just means packages
// get re-checked next boot.
func markInitDone() {
	if err := os.MkdirAll(filepath.Dir(initDoneMarker), 0o755); err != nil {
		log.Printf("init marker: %v", err)
		return
	}
	if err := os.WriteFile(initDoneMarker, []byte("done\n"), 0o644); err != nil {
		log.Printf("init marker: %v", err)
	}
}

// resolveShell falls back when the host shell does not exist in the image
// (a zsh host shell on a stock fedora image, for example).
func resolveShell(shell string) string {
	for _, candidate := range []string{shell, "/bin/bash", "/bin/sh"} {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return "/bin/sh"
}

// ensureUser creates (or adjusts) the container-side user so that it
// matches the host user's name, UID, GID and (resolved) shell. This is what
// makes arbitrary images (fedora, ubuntu, arch) present a familiar user.
func ensureUser(opts InitOptions, shell string) error {
	uid := strconv.Itoa(opts.UID)
	gid := strconv.Itoa(opts.GID)

	// Best-effort group; useradd below references the GID numerically, so a
	// pre-existing group with this GID is fine.
	runLogged("groupadd", "--gid", gid, opts.Username)

	// Some images ship a user squatting on the UID under another name
	// (ubuntu:24.04 has "ubuntu" at 1000). Rename it to the host user,
	// toolbox/distrobox style; the usermod fallback below then converges
	// home, shell, and GID.
	if out, err := run("getent", "passwd", uid); err == nil {
		if existing, _, _ := strings.Cut(strings.TrimSpace(out), ":"); existing != "" && existing != opts.Username {
			log.Printf("renaming existing uid-%s user %q to %q", uid, existing, opts.Username)
			runLogged("usermod", "--login", opts.Username, existing)
		}
	}

	if out, err := run("useradd",
		"--home-dir", opts.Home,
		"--no-create-home",
		"--password", "",
		"--shell", shell,
		"--uid", uid,
		"--gid", gid,
		opts.Username); err != nil {
		// The user (or UID) may already exist, e.g. after a container
		// restart or in an image that ships one; converge with usermod.
		log.Printf("useradd: %s", firstLine(out, err))
		if out, err := run("usermod",
			"--home", opts.Home,
			"--shell", shell,
			"--uid", uid,
			"--gid", gid,
			opts.Username); err != nil {
			return fmt.Errorf("cannot set up user %s: %s", opts.Username, firstLine(out, err))
		}
	}
	return nil
}

// ensureSudoers grants passwordless sudo via a drop-in, distrobox-style.
// Best-effort: the image may not ship sudo at all.
func ensureSudoers(username string) {
	if err := os.MkdirAll("/etc/sudoers.d", 0o755); err != nil {
		log.Printf("sudoers: %v", err)
		return
	}
	content := fmt.Sprintf("%s ALL=(ALL) NOPASSWD:ALL\n", username)
	if err := os.WriteFile("/etc/sudoers.d/bothy", []byte(content), 0o440); err != nil {
		log.Printf("sudoers: %v", err)
	}
}

// installPackages installs the manifest's packages with whichever package
// manager the image ships. Best-effort by design: a failed install is
// logged, not fatal.
func installPackages(packages []string) {
	if len(packages) == 0 {
		return
	}
	switch {
	case commandExists("dnf"):
		runLogged("dnf", append([]string{"install", "-y"}, packages...)...)
	case commandExists("apt-get"):
		if err := os.Setenv("DEBIAN_FRONTEND", "noninteractive"); err != nil {
			log.Printf("packages: %v", err)
		}
		runLogged("apt-get", "update")
		runLogged("apt-get", append([]string{"install", "-y"}, packages...)...)
	case commandExists("pacman"):
		runLogged("pacman", append([]string{"-Sy", "--noconfirm"}, packages...)...)
	default:
		log.Printf("packages: no supported package manager found (dnf, apt-get, pacman); skipping %s", strings.Join(packages, " "))
	}
}

// writeReadyStamp writes the readiness stamp into the bothy home, where the
// host-side enter/run/create commands poll for it. ReadyStampPath is the
// single definition of where it lives; the container-side home mount makes
// the in-container path identical to the host-side one.
func writeReadyStamp(opts InitOptions) error {
	stamp := ReadyStampPath(opts.Home, opts.Stamp)
	dir := filepath.Dir(stamp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	if err := os.WriteFile(stamp, []byte("ready\n"), 0o644); err != nil {
		return fmt.Errorf("cannot write readiness stamp: %w", err)
	}
	// Init runs as root; hand the created paths to the user.
	for _, p := range []string{filepath.Join(opts.Home, ".cache"), dir, stamp} {
		if err := os.Chown(p, opts.UID, opts.GID); err != nil {
			log.Printf("chown %s: %v", p, err)
		}
	}
	return nil
}

// waitForever blocks until SIGTERM/SIGINT, reaping any orphans that get
// reparented to this process (it is PID 1 in the container).
func waitForever() {
	reap := make(chan os.Signal, 16)
	signal.Notify(reap, syscall.SIGCHLD)
	go func() {
		for range reap {
			for {
				pid, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
				if pid <= 0 || err != nil {
					break
				}
			}
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func runLogged(name string, args ...string) {
	if out, err := run(name, args...); err != nil {
		log.Printf("%s: %s", name, firstLine(out, err))
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func firstLine(out string, err error) string {
	if line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]); line != "" {
		return line
	}
	return err.Error()
}
