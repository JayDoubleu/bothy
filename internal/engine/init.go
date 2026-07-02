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
	installPackages(opts.Packages)
	if err := writeReadyStamp(opts); err != nil {
		return err
	}
	log.Printf("ready")

	waitForever()
	return nil
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
		os.Setenv("DEBIAN_FRONTEND", "noninteractive")
		runLogged("apt-get", "update")
		runLogged("apt-get", append([]string{"install", "-y"}, packages...)...)
	case commandExists("pacman"):
		runLogged("pacman", append([]string{"-Sy", "--noconfirm"}, packages...)...)
	default:
		log.Printf("packages: no supported package manager found (dnf, apt-get, pacman); skipping %s", strings.Join(packages, " "))
	}
}

// writeReadyStamp writes the readiness stamp into the bothy home, where the
// host-side enter/run/create commands poll for it.
func writeReadyStamp(opts InitOptions) error {
	dir := filepath.Join(opts.Home, ".cache", "bothy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	stamp := filepath.Join(dir, "ready-"+opts.Stamp)
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
