package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestGood(t *testing.T) {
	m, err := LoadManifest("testdata/minimal.yaml")
	if err != nil {
		t.Fatalf("minimal.yaml: %v", err)
	}
	if m.Image != "registry.fedoraproject.org/fedora:42" {
		t.Errorf("image = %q", m.Image)
	}

	m, err = LoadManifest("testdata/full.yaml")
	if err != nil {
		t.Fatalf("full.yaml: %v", err)
	}
	if m.Hostname != "workbox" || m.Network != "host" {
		t.Errorf("hostname = %q, network = %q", m.Hostname, m.Network)
	}
	if len(m.Mounts) != 2 || m.Mounts[1].Target != "/srv/data" || m.Mounts[1].Mode != "rw" {
		t.Errorf("mounts = %+v", m.Mounts)
	}
	if m.Integration.GUI == nil || !*m.Integration.GUI {
		t.Errorf("integration.gui not set")
	}
	if m.Integration.Audio != nil {
		t.Errorf("integration.audio should be unset")
	}
	if len(m.Packages) != 2 || m.Env["EDITOR"] != "nvim" {
		t.Errorf("packages = %v, env = %v", m.Packages, m.Env)
	}
}

func TestLoadManifestErrors(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"unknown_key.yaml", `unknown key "imgae"`},
		{"bad_mode.yaml", `invalid mode "wr"`},
		{"missing_schema.yaml", `missing required field "schema_version"`},
		{"wrong_schema.yaml", "unsupported schema_version 2"},
		{"bad_network.yaml", `invalid network "bridge"`},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			_, err := LoadManifest(filepath.Join("testdata", tt.file))
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.file) {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}

func TestLoadGlobal(t *testing.T) {
	dir := t.TempDir()

	// Missing file is fine.
	g, err := LoadGlobal(filepath.Join(dir, "config.yaml"))
	if err != nil || g.Defaults != nil {
		t.Fatalf("missing file: g = %+v, err = %v", g, err)
	}

	path := filepath.Join(dir, "config.yaml")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("defaults:\n  network: host\n  env:\n    FOO: bar\n")
	g, err = LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if g.Defaults == nil || g.Defaults.Network != "host" || g.Defaults.Env["FOO"] != "bar" {
		t.Errorf("defaults = %+v", g.Defaults)
	}

	// schema_version does not belong in defaults.
	write("defaults:\n  schema_version: 1\n")
	if _, err := LoadGlobal(path); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("expected schema_version error, got %v", err)
	}

	// Unknown top-level keys are errors too.
	write("default:\n  network: host\n")
	if _, err := LoadGlobal(path); err == nil || !strings.Contains(err.Error(), `unknown key "default"`) {
		t.Errorf("expected unknown key error, got %v", err)
	}
}
