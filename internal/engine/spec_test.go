package engine

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jaydoubleu/bothy/internal/config"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

func testConfig() *config.Config {
	return &config.Config{
		Image:    "registry.fedoraproject.org/fedora:42",
		Hostname: "work",
		Home:     "/testhome/.local/share/bothy/homes/work",
		Mounts: []config.ResolvedMount{
			{Source: "/testhome/.config/nvim", Target: "/home/alice/.config/nvim", Mode: "ro"},
			{Source: "/opt/data", Target: "/srv/data", Mode: "rw"},
		},
		Network:         config.NetworkNone,
		Env:             map[string]string{"EDITOR": "nvim"},
		Packages:        []string{"git", "neovim"},
		ExtraPodmanArgs: []string{"--memory=4g"},
	}
}

func testUser() User {
	return User{Username: "alice", UID: 1000, GID: 1000, Shell: "/usr/bin/zsh"}
}

func TestBuildCreateSpec(t *testing.T) {
	cfg := testConfig()
	spec, warnings, err := BuildCreateSpec("work", cfg, "/usr/bin/bothy", testUser(), "teststamp")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	if spec.Name != "bothy-work" || spec.Hostname != "work" || spec.Image != cfg.Image {
		t.Errorf("name/hostname/image: %q %q %q", spec.Name, spec.Hostname, spec.Image)
	}
	if spec.Userns != "keep-id" || spec.User != "root:root" || !spec.SecurityLabelDisable || !spec.UlimitHost {
		t.Errorf("hardening flags: %+v", spec)
	}
	if spec.Network != "none" {
		t.Errorf("network = %q", spec.Network)
	}

	hash, err := ConfigHash(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wantLabels := map[string]string{
		"com.github.jaydoubleu.bothy.managed":        "true",
		"com.github.jaydoubleu.bothy.name":           "work",
		"com.github.jaydoubleu.bothy.schema-version": "1",
		"com.github.jaydoubleu.bothy.home":           cfg.Home,
		"com.github.jaydoubleu.bothy.config-hash":    hash,
		"com.github.jaydoubleu.bothy.stamp":          "teststamp",
	}
	if !reflect.DeepEqual(spec.Labels, wantLabels) {
		t.Errorf("labels = %v, want %v", spec.Labels, wantLabels)
	}

	wantMounts := []runtime.Mount{
		{Source: cfg.Home, Target: "/home/alice"},
		{Source: "/usr/bin/bothy", Target: BinaryMountPath, ReadOnly: true},
		{Source: "/testhome/.config/nvim", Target: "/home/alice/.config/nvim", ReadOnly: true},
		{Source: "/opt/data", Target: "/srv/data"},
	}
	if !reflect.DeepEqual(spec.Mounts, wantMounts) {
		t.Errorf("mounts = %+v, want %+v", spec.Mounts, wantMounts)
	}

	wantEntrypoint := []string{
		BinaryMountPath, "init",
		"--uid", "1000", "--gid", "1000",
		"--username", "alice",
		"--shell", "/usr/bin/zsh",
		"--home", "/home/alice",
		"--stamp", "teststamp",
		"--package", "git", "--package", "neovim",
	}
	if !reflect.DeepEqual(spec.Entrypoint, wantEntrypoint) {
		t.Errorf("entrypoint = %q, want %q", spec.Entrypoint, wantEntrypoint)
	}
}

func TestBuildCreateSpecNetworkMapping(t *testing.T) {
	for network, want := range map[string]string{
		config.NetworkPrivate: "",
		config.NetworkHost:    "host",
		config.NetworkNone:    "none",
	} {
		cfg := testConfig()
		cfg.Network = network
		spec, _, err := BuildCreateSpec("work", cfg, "/usr/bin/bothy", testUser(), "teststamp")
		if err != nil {
			t.Fatal(err)
		}
		if spec.Network != want {
			t.Errorf("network %q maps to %q, want %q", network, spec.Network, want)
		}
	}
}

func TestBuildCreateSpecStubbedIntegrationsWarn(t *testing.T) {
	cfg := testConfig()
	cfg.Integration.GUI = true
	cfg.Integration.SSHAgent = true
	spec, warnings, err := BuildCreateSpec("work", cfg, "/usr/bin/bothy", testUser(), "teststamp")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "not implemented yet") {
			t.Errorf("warning %q", w)
		}
	}
	// Stubs must not add mounts yet.
	if len(spec.Mounts) != 4 {
		t.Errorf("stubbed toggles added mounts: %+v", spec.Mounts)
	}
}

func TestConfigHashDeterministicAndSensitive(t *testing.T) {
	a, err := ConfigHash(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	b, err := ConfigHash(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("hash is not deterministic: %s vs %s", a, b)
	}
	changed := testConfig()
	changed.Env["NEW"] = "value"
	c, err := ConfigHash(changed)
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Errorf("hash did not change with the config")
	}
}
