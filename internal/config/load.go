package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadManifest reads and strictly parses a manifest file. All errors carry
// the file path and, where possible, a line number and field name.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := parseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if m.SchemaVersion == 0 {
		return nil, fmt.Errorf("%s: missing required field \"schema_version\" (add \"schema_version: %d\")", path, SupportedSchemaVersion)
	}
	if m.SchemaVersion != SupportedSchemaVersion {
		return nil, fmt.Errorf("%s: unsupported schema_version %d (this build of bothy supports %d)", path, m.SchemaVersion, SupportedSchemaVersion)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// LoadGlobal reads the global config file. A missing file is not an error;
// it yields an empty config.
func LoadGlobal(path string) (*GlobalConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &GlobalConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var g GlobalConfig
	if err := dec.Decode(&g); err != nil {
		if errors.Is(err, io.EOF) {
			return &GlobalConfig{}, nil
		}
		return nil, fmt.Errorf("%s: %w", path, friendlyYAMLError(err))
	}
	if g.Defaults != nil {
		if g.Defaults.SchemaVersion != 0 {
			return nil, fmt.Errorf("%s: defaults: \"schema_version\" belongs in manifests, not in the global defaults", path)
		}
		if err := g.Defaults.validate(); err != nil {
			return nil, fmt.Errorf("%s: defaults: %w", path, err)
		}
	}
	return &g, nil
}

func parseManifest(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("file is empty")
		}
		return nil, friendlyYAMLError(err)
	}
	return &m, nil
}

// validate checks the fields whose valid values are known per-layer, so the
// error can point at the file the bad value came from.
func (m *Manifest) validate() error {
	for i, mt := range m.Mounts {
		if strings.TrimSpace(mt.Source) == "" {
			return fmt.Errorf("mounts[%d]: \"source\" is required", i)
		}
		if err := validMode(mt.Mode, true); err != nil {
			return fmt.Errorf("mounts[%d]: %w", i, err)
		}
	}
	if err := validNetwork(m.Network, true); err != nil {
		return err
	}
	return nil
}

func validMode(mode string, allowEmpty bool) error {
	switch mode {
	case "ro", "rw":
		return nil
	case "":
		if allowEmpty {
			return nil
		}
	}
	return fmt.Errorf("invalid mode %q (expected \"ro\" or \"rw\")", mode)
}

func validNetwork(network string, allowEmpty bool) error {
	switch network {
	case NetworkHost, NetworkPrivate, NetworkNone:
		return nil
	case "":
		if allowEmpty {
			return nil
		}
	}
	return fmt.Errorf("invalid network %q (expected \"host\", \"private\" or \"none\")", network)
}

var unknownFieldRe = regexp.MustCompile(`line (\d+): field (\S+) not found in type \S+`)

// friendlyYAMLError rewrites yaml.v3 errors into one-line messages that do
// not leak Go type names.
func friendlyYAMLError(err error) error {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		msgs := make([]string, 0, len(typeErr.Errors))
		for _, e := range typeErr.Errors {
			if m := unknownFieldRe.FindStringSubmatch(e); m != nil {
				msgs = append(msgs, fmt.Sprintf("line %s: unknown key %q", m[1], m[2]))
			} else {
				msgs = append(msgs, strings.TrimPrefix(e, "yaml: "))
			}
		}
		return errors.New(strings.Join(msgs, "; "))
	}
	return fmt.Errorf("invalid YAML: %s", strings.TrimPrefix(err.Error(), "yaml: "))
}
