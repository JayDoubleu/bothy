// Package config defines the bothy manifest schema (schema_version 1),
// strict loading, layer merging, and validation.
package config

// SupportedSchemaVersion is the only manifest schema version this build
// understands.
const SupportedSchemaVersion = 1

// Manifest mirrors the on-disk YAML schema. It is also the shape of the
// "defaults:" block in the global config and of CLI flag overlays, so
// every field must be able to represent "unset": strings and slices use
// their zero value, integration toggles use pointers.
type Manifest struct {
	SchemaVersion   int               `yaml:"schema_version,omitempty"`
	Image           string            `yaml:"image,omitempty"`
	Hostname        string            `yaml:"hostname,omitempty"`
	Home            string            `yaml:"home,omitempty"`
	Mounts          []Mount           `yaml:"mounts,omitempty"`
	Integration     Integration       `yaml:"integration,omitempty"`
	Network         string            `yaml:"network,omitempty"`
	Env             map[string]string `yaml:"env,omitempty"`
	Packages        []string          `yaml:"packages,omitempty"`
	ExtraPodmanArgs []string          `yaml:"extra_podman_args,omitempty"`
}

// Mount declares a single host path shared into the bothy. Target defaults
// to the source path (expanded against the container home) and Mode
// defaults to "ro".
type Mount struct {
	Source string `yaml:"source"`
	Target string `yaml:"target,omitempty"`
	Mode   string `yaml:"mode,omitempty"`
}

// Integration holds the host-integration toggles. Pointers distinguish
// "unset" from an explicit false so a manifest can switch off a toggle
// enabled in the global defaults.
type Integration struct {
	GUI      *bool `yaml:"gui,omitempty"`
	Audio    *bool `yaml:"audio,omitempty"`
	DBus     *bool `yaml:"dbus,omitempty"`
	Devices  *bool `yaml:"devices,omitempty"`
	Fonts    *bool `yaml:"fonts,omitempty"`
	SSHAgent *bool `yaml:"ssh_agent,omitempty"`
	Timezone *bool `yaml:"timezone,omitempty"`
}

// GlobalConfig is ~/.config/bothy/config.yaml.
type GlobalConfig struct {
	Defaults *Manifest `yaml:"defaults,omitempty"`
}

// Config is a fully resolved configuration: all layers merged, defaults
// filled in, paths expanded. It is what gets hashed into the config-hash
// label and turned into a container spec, so its serialized form must be
// deterministic (fixed field order; the JSON encoder sorts map keys).
type Config struct {
	Image           string              `yaml:"image" json:"image"`
	Hostname        string              `yaml:"hostname" json:"hostname"`
	Home            string              `yaml:"home" json:"home"`
	Mounts          []ResolvedMount     `yaml:"mounts" json:"mounts"`
	Integration     ResolvedIntegration `yaml:"integration" json:"integration"`
	Network         string              `yaml:"network" json:"network"`
	Env             map[string]string   `yaml:"env" json:"env"`
	Packages        []string            `yaml:"packages" json:"packages"`
	ExtraPodmanArgs []string            `yaml:"extra_podman_args" json:"extra_podman_args"`
}

// ResolvedMount always has all three fields populated with expanded,
// absolute paths.
type ResolvedMount struct {
	Source string `yaml:"source" json:"source"`
	Target string `yaml:"target" json:"target"`
	Mode   string `yaml:"mode" json:"mode"`
}

// ResolvedIntegration is Integration with defaults applied: everything is
// off unless a layer switched it on.
type ResolvedIntegration struct {
	GUI      bool `yaml:"gui" json:"gui"`
	Audio    bool `yaml:"audio" json:"audio"`
	DBus     bool `yaml:"dbus" json:"dbus"`
	Devices  bool `yaml:"devices" json:"devices"`
	Fonts    bool `yaml:"fonts" json:"fonts"`
	SSHAgent bool `yaml:"ssh_agent" json:"ssh_agent"`
	Timezone bool `yaml:"timezone" json:"timezone"`
}

// Network values.
const (
	NetworkHost    = "host"
	NetworkPrivate = "private"
	NetworkNone    = "none"
)
