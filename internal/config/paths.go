package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigDir returns ~/.config/bothy.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "bothy"), nil
}

// GlobalConfigPath returns ~/.config/bothy/config.yaml.
func GlobalConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// DefaultManifestPath returns ~/.config/bothy/<name>.yaml.
func DefaultManifestPath(name string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".yaml"), nil
}

// DefaultHomePath returns ~/.local/share/bothy/homes/<name>, the host-side
// home directory used when the manifest does not set "home:".
func DefaultHomePath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "bothy", "homes", name), nil
}

// ExpandHostPath expands a leading ~ against the host home directory.
func ExpandHostPath(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot expand %q: %w", p, err)
	}
	return expandAgainst(p, home), nil
}

// expandAgainst expands a leading ~ against the given home directory.
func expandAgainst(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
