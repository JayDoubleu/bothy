package config

import (
	"reflect"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestResolvePrecedenceAndDefaults(t *testing.T) {
	t.Setenv("HOME", "/testhome")

	global := &Manifest{
		Image:    "global-image",
		Network:  NetworkHost,
		Env:      map[string]string{"FROM_GLOBAL": "1", "SHARED": "global"},
		Packages: []string{"git"},
		Integration: Integration{
			GUI:   boolPtr(true),
			Audio: boolPtr(true),
		},
	}
	manifest := &Manifest{
		Image: "manifest-image",
		Env:   map[string]string{"SHARED": "manifest"},
		Mounts: []Mount{
			{Source: "~/.config/nvim"},
			{Source: "/opt/data", Target: "/srv/data", Mode: "rw"},
			{Source: "~/devel", Mode: "overlay"},
		},
		Packages: []string{"neovim"},
		Integration: Integration{
			GUI: boolPtr(false), // explicit false must override global true
		},
	}
	flags := &Manifest{Network: NetworkNone}

	cfg, err := Resolve(ResolveOptions{Name: "work", Username: "alice"}, global, manifest, flags)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Image != "manifest-image" {
		t.Errorf("image = %q", cfg.Image)
	}
	if cfg.Network != NetworkNone {
		t.Errorf("network = %q (flags should win)", cfg.Network)
	}
	if cfg.Hostname != "work" {
		t.Errorf("hostname = %q (should default to the name)", cfg.Hostname)
	}
	if cfg.Home != "/testhome/.local/share/bothy/homes/work" {
		t.Errorf("home = %q", cfg.Home)
	}
	wantEnv := map[string]string{"FROM_GLOBAL": "1", "SHARED": "manifest"}
	if !reflect.DeepEqual(cfg.Env, wantEnv) {
		t.Errorf("env = %v, want %v", cfg.Env, wantEnv)
	}
	if !reflect.DeepEqual(cfg.Packages, []string{"git", "neovim"}) {
		t.Errorf("packages = %v (layers should concatenate)", cfg.Packages)
	}
	wantMounts := []ResolvedMount{
		{Source: "/testhome/.config/nvim", Target: "/home/alice/.config/nvim", Mode: "ro"},
		{Source: "/opt/data", Target: "/srv/data", Mode: "rw"},
		{Source: "/testhome/devel", Target: "/home/alice/devel", Mode: "overlay"},
	}
	if !reflect.DeepEqual(cfg.Mounts, wantMounts) {
		t.Errorf("mounts = %+v, want %+v", cfg.Mounts, wantMounts)
	}
	if cfg.Integration.GUI {
		t.Errorf("integration.gui = true (manifest false should override global true)")
	}
	if !cfg.Integration.Audio {
		t.Errorf("integration.audio = false (global true should survive)")
	}
	if cfg.Integration.DBus {
		t.Errorf("integration.dbus = true (should default to false)")
	}
}

func TestResolveBuiltinDefaults(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	cfg, err := Resolve(ResolveOptions{Name: "scratch", Username: "alice"}, &Manifest{Image: "img"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network != NetworkPrivate {
		t.Errorf("network = %q, want private", cfg.Network)
	}
	if cfg.Integration != (ResolvedIntegration{}) {
		t.Errorf("integration = %+v, want all false", cfg.Integration)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Setenv("HOME", "/testhome")

	if _, err := Resolve(ResolveOptions{Name: "x", Username: "alice"}); err == nil || !strings.Contains(err.Error(), "image is required") {
		t.Errorf("missing image: %v", err)
	}

	_, err := Resolve(ResolveOptions{Name: "x", Username: "alice"}, &Manifest{
		Image:  "img",
		Mounts: []Mount{{Source: "relative/path"}},
	})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("relative mount source: %v", err)
	}
}

func TestResolveRejectsVolumeUnsafePaths(t *testing.T) {
	t.Setenv("HOME", "/testhome")

	// podman --volume uses ':' and ',' as separators with no escaping, so
	// such paths must be rejected up front with a clear error.
	cases := []struct {
		name     string
		manifest *Manifest
		wantIn   string
	}{
		{"colon in mount source", &Manifest{Image: "img", Mounts: []Mount{{Source: "/data/a:b"}}}, "mounts[0]: source"},
		{"comma in mount source", &Manifest{Image: "img", Mounts: []Mount{{Source: "/data/a,b"}}}, "mounts[0]: source"},
		{"colon in mount target", &Manifest{Image: "img", Mounts: []Mount{{Source: "/data/ok", Target: "/dst/a:b"}}}, "mounts[0]: target"},
		{"comma in home", &Manifest{Image: "img", Home: "/homes/a,b"}, "home"},
	}
	for _, tc := range cases {
		_, err := Resolve(ResolveOptions{Name: "x", Username: "alice"}, tc.manifest)
		if err == nil || !strings.Contains(err.Error(), tc.wantIn) || !strings.Contains(err.Error(), "podman volume") {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}

	// The characters remain fine outside volume-bound fields.
	cfg, err := Resolve(ResolveOptions{Name: "x", Username: "alice"}, &Manifest{
		Image: "img",
		Env:   map[string]string{"PATHISH": "/a:/b,c"},
	})
	if err != nil || cfg.Env["PATHISH"] != "/a:/b,c" {
		t.Errorf("env values must not be restricted: %v %v", cfg, err)
	}
}
