package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewPodmanEnvOverride(t *testing.T) {
	t.Setenv("BOTHY_PODMAN", "")
	p := NewPodman()
	if !reflect.DeepEqual(p.Command, []string{"podman"}) || p.explicit {
		t.Errorf("default: %+v", p)
	}

	t.Setenv("BOTHY_PODMAN", "flatpak-spawn --host podman")
	p = NewPodman()
	if !reflect.DeepEqual(p.Command, []string{"flatpak-spawn", "--host", "podman"}) || !p.explicit {
		t.Errorf("override: %+v", p)
	}
}

func TestPreflightRefusesNestedPodman(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "containerenv")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := containerMarkers
	containerMarkers = []string{marker}
	t.Cleanup(func() { containerMarkers = old })

	p := &Podman{Command: []string{"podman"}}
	err := p.Preflight(context.Background())
	if err == nil || !strings.Contains(err.Error(), "BOTHY_PODMAN") {
		t.Errorf("expected nested-container refusal, got %v", err)
	}

	// An explicit BOTHY_PODMAN command skips the guard (and then fails on
	// lookup of a nonexistent binary, which is fine for this test).
	p = &Podman{Command: []string{"definitely-not-podman"}, explicit: true}
	err = p.Preflight(context.Background())
	if err == nil || strings.Contains(err.Error(), "inside a container") {
		t.Errorf("explicit command must skip the guard, got %v", err)
	}
}

func TestCreateArgs(t *testing.T) {
	spec := CreateSpec{
		Name:     "bothy-work",
		Hostname: "work",
		Labels: map[string]string{
			"com.github.jaydoubleu.bothy.managed": "true",
			"com.github.jaydoubleu.bothy.name":    "work",
		},
		Userns:               "keep-id",
		User:                 "root:root",
		SecurityLabelDisable: true,
		UlimitHost:           true,
		Network:              "none",
		Env:                  map[string]string{"EDITOR": "nvim"},
		Mounts: []Mount{
			{Source: "/host/home", Target: "/home/alice"},
			{Source: "/usr/bin/bothy", Target: "/usr/libexec/bothy", ReadOnly: true},
		},
		ExtraArgs:  []string{"--memory=4g"},
		Image:      "fedora:42",
		Entrypoint: []string{"/usr/libexec/bothy", "init", "--uid", "1000"},
	}
	want := []string{
		"create", "--name", "bothy-work",
		"--hostname", "work",
		"--label", "com.github.jaydoubleu.bothy.managed=true",
		"--label", "com.github.jaydoubleu.bothy.name=work",
		"--userns=keep-id",
		"--user", "root:root",
		"--security-opt", "label=disable",
		"--ulimit", "host",
		"--network", "none",
		"--env", "EDITOR=nvim",
		"--volume", "/host/home:/home/alice:rw",
		"--volume", "/usr/bin/bothy:/usr/libexec/bothy:ro",
		"--memory=4g",
		"fedora:42",
		"/usr/libexec/bothy", "init", "--uid", "1000",
	}
	if got := createArgs(spec); !reflect.DeepEqual(got, want) {
		t.Errorf("createArgs:\n got  %q\n want %q", got, want)
	}
}

func TestCreateArgsPrivateNetworkOmitsFlag(t *testing.T) {
	args := createArgs(CreateSpec{Name: "bothy-x", Image: "img"})
	for _, a := range args {
		if a == "--network" {
			t.Errorf("private network must not emit --network: %q", args)
		}
	}
}

func TestExecArgs(t *testing.T) {
	spec := ExecSpec{
		Container:   "bothy-work",
		User:        "alice",
		WorkDir:     "/home/alice",
		Env:         map[string]string{"HOME": "/home/alice", "USER": "alice"},
		Interactive: true,
		TTY:         true,
		Command:     []string{"whoami"},
	}
	want := []string{
		"exec", "--interactive", "--tty",
		"--user", "alice",
		"--workdir", "/home/alice",
		"--env", "HOME=/home/alice",
		"--env", "USER=alice",
		"bothy-work", "whoami",
	}
	if got := execArgs(spec); !reflect.DeepEqual(got, want) {
		t.Errorf("execArgs:\n got  %q\n want %q", got, want)
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	if got := lastNonEmptyLine("a\nError: boom\n\n"); got != "Error: boom" {
		t.Errorf("got %q", got)
	}
	if got := lastNonEmptyLine("\n\n"); got != "" {
		t.Errorf("got %q", got)
	}
}
