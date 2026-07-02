package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jaydoubleu/bothy/internal/engine"
	"github.com/jaydoubleu/bothy/internal/runtime"
)

// runCLI executes the root command against a Fake runtime.
func runCLI(t *testing.T, fake *runtime.Fake, args ...string) error {
	t.Helper()
	old := newRuntime
	newRuntime = func() runtime.Runtime { return fake }
	t.Cleanup(func() { newRuntime = old })
	root := newRootCmd()
	root.SetArgs(args)
	return root.Execute()
}

func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreateBuildsExpectedSpec(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp) // isolate from any real global config

	home := filepath.Join(tmp, "bothy-home")
	manifest := writeManifest(t, tmp, "work.yaml",
		"schema_version: 1\nimage: fedora:42\nhome: "+home+"\nnetwork: none\n")

	fake := runtime.NewFake()
	// Pin the stamp nonce and pre-write the readiness stamp create waits
	// for; the Fake has no init process to write it.
	oldStamp := newStamp
	newStamp = func() (string, error) { return "teststamp", nil }
	t.Cleanup(func() { newStamp = oldStamp })
	stamp := engine.ReadyStampPath(home, "teststamp")
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stamp, []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runCLI(t, fake, "create", "work", "-f", manifest); err != nil {
		t.Fatal(err)
	}

	if len(fake.CreatedSpecs) != 1 {
		t.Fatalf("created %d containers", len(fake.CreatedSpecs))
	}
	spec := fake.CreatedSpecs[0]
	if spec.Name != "bothy-work" || spec.Image != "fedora:42" || spec.Network != "none" {
		t.Errorf("spec = %+v", spec)
	}
	if spec.Labels[engine.LabelHome] != home {
		t.Errorf("home label = %q, want %q", spec.Labels[engine.LabelHome], home)
	}
	if len(spec.Mounts) < 2 || spec.Mounts[0].Source != home || spec.Mounts[0].ReadOnly {
		t.Errorf("home mount = %+v", spec.Mounts)
	}
	if spec.Mounts[1].Target != engine.BinaryMountPath || !spec.Mounts[1].ReadOnly {
		t.Errorf("binary mount = %+v", spec.Mounts[1])
	}
	if fi, err := os.Stat(home); err != nil || !fi.IsDir() {
		t.Errorf("home dir was not created: %v", err)
	}
	if len(fake.Started) != 1 {
		t.Errorf("started = %v", fake.Started)
	}

	// Creating again must fail: the container exists.
	if err := runCLI(t, fake, "create", "work", "-f", manifest); err == nil {
		t.Errorf("second create should fail")
	}
}

func TestCreateOverlayMountMakesDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	home := filepath.Join(tmp, "bothy-home")
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := writeManifest(t, tmp, "ovl.yaml",
		"schema_version: 1\nimage: fedora:42\nhome: "+home+"\nmounts:\n  - source: "+src+"\n    target: /mnt/ovl\n    mode: overlay\n")

	fake := runtime.NewFake()
	oldStamp := newStamp
	newStamp = func() (string, error) { return "teststamp", nil }
	t.Cleanup(func() { newStamp = oldStamp })
	stamp := engine.ReadyStampPath(home, "teststamp")
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stamp, []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runCLI(t, fake, "create", "ovl", "-f", manifest); err != nil {
		t.Fatal(err)
	}

	spec := fake.CreatedSpecs[0]
	mount := spec.Mounts[2]
	if !mount.Overlay || mount.Source != src || mount.Target != "/mnt/ovl" {
		t.Errorf("overlay mount = %+v", mount)
	}
	for _, dir := range []string{mount.UpperDir, mount.WorkDir} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("overlay dir %s not created: %v", dir, err)
		}
	}
}

func TestRmKeepHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	home := filepath.Join(tmp, "bothy-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := runtime.NewFake()
	fake.Containers["bothy-work"] = &runtime.Container{
		ID:   "abc",
		Name: "bothy-work",
		Labels: map[string]string{
			engine.LabelManaged: "true",
			engine.LabelName:    "work",
			engine.LabelHome:    home,
		},
	}

	if err := runCLI(t, fake, "rm", "work", "--keep-home"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Removed) != 1 || fake.Removed[0] != "bothy-work" {
		t.Errorf("removed = %v", fake.Removed)
	}
	if _, err := os.Stat(home); err != nil {
		t.Errorf("home should survive --keep-home: %v", err)
	}
}

func TestRmForceDeletesHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	home := filepath.Join(tmp, "bothy-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := runtime.NewFake()
	fake.Containers["bothy-work"] = &runtime.Container{
		ID:     "abc",
		Name:   "bothy-work",
		Labels: map[string]string{engine.LabelHome: home},
	}

	if err := runCLI(t, fake, "rm", "work", "--force"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("home should be deleted: %v", err)
	}
}

func TestRmRefusesRealHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	fake := runtime.NewFake()
	fake.Containers["bothy-evil"] = &runtime.Container{
		ID:     "abc",
		Name:   "bothy-evil",
		Labels: map[string]string{engine.LabelHome: tmp},
	}
	if err := runCLI(t, fake, "rm", "evil", "--force"); err == nil {
		t.Fatal("rm must refuse to delete the real home directory")
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("real home was touched: %v", err)
	}
}

func TestRunUnknownBothy(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	fake := runtime.NewFake()
	if err := runCLI(t, fake, "run", "ghost", "--", "true"); err == nil {
		t.Fatal("run against a missing bothy should fail")
	}
}
