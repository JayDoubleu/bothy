package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// minPodmanMajor is the oldest podman major version bothy works with.
// Rootless --userns=keep-id in the form bothy relies on needs podman 4.
const minPodmanMajor = 4

// Podman implements Runtime by shelling out to podman.
type Podman struct {
	// Command is the podman command, possibly with a wrapper prefix
	// (e.g. ["flatpak-spawn", "--host", "podman"]).
	Command []string
	// explicit marks a user-chosen command (BOTHY_PODMAN), which skips the
	// nested-container guard in Preflight.
	explicit bool
}

// NewPodman returns a Podman runtime. By default it uses the "podman"
// binary from PATH; the BOTHY_PODMAN environment variable overrides the
// command (split on whitespace, so wrappers like "flatpak-spawn --host
// podman" work) and doubles as the opt-in for running bothy inside a
// container.
func NewPodman() *Podman {
	if cmd := os.Getenv("BOTHY_PODMAN"); cmd != "" {
		return &Podman{Command: strings.Fields(cmd), explicit: true}
	}
	return &Podman{Command: []string{"podman"}}
}

// containerMarkers are files whose presence means this process is running
// inside a container rather than on the host.
var containerMarkers = []string{"/run/.containerenv", "/.dockerenv"}

func insideContainer() bool {
	for _, marker := range containerMarkers {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

func (p *Podman) cmd(ctx context.Context, args ...string) *exec.Cmd {
	full := append(append([]string{}, p.Command[1:]...), args...)
	return exec.CommandContext(ctx, p.Command[0], full...)
}

func (p *Podman) Preflight(ctx context.Context) error {
	// A rootless podman found inside a container (toolbox, distrobox, ...)
	// typically shares $HOME and $XDG_RUNTIME_DIR with the host's podman.
	// Driving it corrupts the host's pause-process state and can kill every
	// rootless container on the host, including the one bothy is running
	// in. Refuse unless the user picked the command explicitly.
	if !p.explicit && insideContainer() {
		return fmt.Errorf("bothy appears to be running inside a container; using the container's own podman would corrupt the host's rootless podman state.\n" +
			"Run bothy on the host instead, or set BOTHY_PODMAN to a command that reaches the host podman\n" +
			"(inside a toolbox: BOTHY_PODMAN=\"flatpak-spawn --host podman\")")
	}
	if _, err := exec.LookPath(p.Command[0]); err != nil {
		return fmt.Errorf("%s not found in PATH; bothy requires podman >= %d.0", p.Command[0], minPodmanMajor)
	}
	out, err := p.output(ctx, "version", "--format", "json")
	if err != nil {
		return fmt.Errorf("podman is not usable: %w", err)
	}
	var v struct {
		Client struct {
			Version string `json:"Version"`
		} `json:"Client"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return fmt.Errorf("cannot parse podman version output: %w", err)
	}
	major, _, _ := strings.Cut(v.Client.Version, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return fmt.Errorf("cannot parse podman version %q", v.Client.Version)
	}
	if n < minPodmanMajor {
		return fmt.Errorf("bothy requires podman >= %d.0 for rootless --userns=keep-id support; found %s", minPodmanMajor, v.Client.Version)
	}
	return nil
}

func (p *Podman) Create(ctx context.Context, spec CreateSpec) (string, error) {
	// Stream stderr so image pull progress is visible; keep a copy for the
	// error message.
	var stdout, stderr bytes.Buffer
	cmd := p.cmd(ctx, createArgs(spec)...)
	cmd.Stdout = &stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		return "", commandError("create", &stderr, err)
	}
	// The container ID is the last non-empty stdout line.
	lines := strings.Fields(stdout.String())
	if len(lines) == 0 {
		return "", fmt.Errorf("podman create: no container ID in output")
	}
	return lines[len(lines)-1], nil
}

func (p *Podman) Start(ctx context.Context, container string) error {
	_, err := p.output(ctx, "start", container)
	return err
}

func (p *Podman) Stop(ctx context.Context, container string) error {
	_, err := p.output(ctx, "stop", container)
	return err
}

func (p *Podman) Remove(ctx context.Context, container string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, container)
	_, err := p.output(ctx, args...)
	return err
}

func (p *Podman) Exec(ctx context.Context, spec ExecSpec) error {
	cmd := p.cmd(ctx, execArgs(spec)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExitError{Code: exitErr.ExitCode()}
	}
	return err
}

func (p *Podman) List(ctx context.Context, labelFilters map[string]string) ([]Container, error) {
	args := []string{"ps", "--all", "--format", "json"}
	for _, k := range sortedKeys(labelFilters) {
		args = append(args, "--filter", "label="+k+"="+labelFilters[k])
	}
	out, err := p.output(ctx, args...)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("cannot parse podman ps output: %w", err)
	}
	containers := make([]Container, 0, len(entries))
	for _, e := range entries {
		name := ""
		if len(e.Names) > 0 {
			name = e.Names[0]
		}
		containers = append(containers, Container{
			ID:      e.ID,
			Name:    name,
			Image:   e.Image,
			Running: e.State == "running",
			Status:  e.Status,
			Labels:  e.Labels,
		})
	}
	return containers, nil
}

func (p *Podman) Inspect(ctx context.Context, container string) (*Container, error) {
	out, err := p.output(ctx, "container", "inspect", "--format", "json", container)
	if err != nil {
		if strings.Contains(err.Error(), "no such container") {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, container)
		}
		return nil, err
	}
	var entries []struct {
		ID        string `json:"Id"`
		Name      string `json:"Name"`
		ImageName string `json:"ImageName"`
		Config    struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
		} `json:"State"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("cannot parse podman inspect output: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, container)
	}
	e := entries[0]
	return &Container{
		ID:      e.ID,
		Name:    strings.TrimPrefix(e.Name, "/"),
		Image:   e.ImageName,
		Running: e.State.Running,
		Status:  e.State.Status,
		Labels:  e.Config.Labels,
	}, nil
}

// output runs podman with the given args, capturing stdout. On failure the
// error contains the last stderr line, not a stack trace.
func (p *Podman) output(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := p.cmd(ctx, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", commandError(args[0], &stderr, err)
	}
	return stdout.String(), nil
}

func commandError(verb string, stderr *bytes.Buffer, err error) error {
	msg := lastNonEmptyLine(stderr.String())
	if msg == "" {
		return fmt.Errorf("podman %s: %w", verb, err)
	}
	return fmt.Errorf("podman %s: %s", verb, strings.TrimPrefix(msg, "Error: "))
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// createArgs builds the podman create argument list. Map-driven flags are
// emitted in sorted key order so the command line is deterministic.
func createArgs(spec CreateSpec) []string {
	args := []string{"create", "--name", spec.Name}
	if spec.Hostname != "" {
		args = append(args, "--hostname", spec.Hostname)
	}
	for _, k := range sortedKeys(spec.Labels) {
		args = append(args, "--label", k+"="+spec.Labels[k])
	}
	if spec.Userns != "" {
		args = append(args, "--userns="+spec.Userns)
	}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	if spec.SecurityLabelDisable {
		args = append(args, "--security-opt", "label=disable")
	}
	if spec.UlimitHost {
		args = append(args, "--ulimit", "host")
	}
	if spec.Network != "" {
		args = append(args, "--network", spec.Network)
	}
	for _, k := range sortedKeys(spec.Env) {
		args = append(args, "--env", k+"="+spec.Env[k])
	}
	for _, m := range spec.Mounts {
		var opts string
		switch {
		case m.Overlay:
			opts = ":O"
			if m.UpperDir != "" {
				opts += ",upperdir=" + m.UpperDir + ",workdir=" + m.WorkDir
			}
		case m.ReadOnly:
			opts = ":ro"
		default:
			opts = ":rw"
		}
		args = append(args, "--volume", m.Source+":"+m.Target+opts)
	}
	// The entrypoint must be forced with --entrypoint (JSON-array exec
	// form): passed as the container command instead, an image-defined
	// ENTRYPOINT (postgres, node, ...) would swallow it as "$@".
	if len(spec.Entrypoint) > 0 {
		ep, _ := json.Marshal(spec.Entrypoint) // a []string cannot fail to marshal
		args = append(args, "--entrypoint", string(ep))
	}
	args = append(args, spec.ExtraArgs...)
	args = append(args, spec.Image)
	return args
}

func execArgs(spec ExecSpec) []string {
	args := []string{"exec"}
	if spec.Interactive {
		args = append(args, "--interactive")
	}
	if spec.TTY {
		args = append(args, "--tty")
	}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	if spec.WorkDir != "" {
		args = append(args, "--workdir", spec.WorkDir)
	}
	for _, k := range sortedKeys(spec.Env) {
		args = append(args, "--env", k+"="+spec.Env[k])
	}
	args = append(args, spec.Container)
	args = append(args, spec.Command...)
	return args
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
