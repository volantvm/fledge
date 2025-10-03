// Package config provides configuration parsing and validation for fledge.toml.
package config

// Config represents the complete fledge.toml configuration.
type Config struct {
	Version    string            `toml:"version"`
	Strategy   string            `toml:"strategy"`
	Agent      *AgentConfig      `toml:"agent,omitempty"`
	Source     SourceConfig      `toml:"source"`
	Filesystem *FilesystemConfig `toml:"filesystem,omitempty"`
	Mappings   map[string]string `toml:"mappings,omitempty"`
}

// AgentConfig defines how to source the kestrel agent binary.
type AgentConfig struct {
	SourceStrategy string `toml:"source_strategy"`

	// For "release" strategy
	Version string `toml:"version,omitempty"`

	// For "local" strategy
	Path string `toml:"path,omitempty"`

	// For "http" strategy
	URL      string `toml:"url,omitempty"`
	Checksum string `toml:"checksum,omitempty"`
}

// SourceConfig defines the source for the build strategy.
// The actual fields used depend on the strategy type.
type SourceConfig struct {
	// For "oci_rootfs" strategy
	Image string `toml:"image,omitempty"`

	// For "initramfs" strategy
	BusyboxURL    string `toml:"busybox_url,omitempty"`
	BusyboxSHA256 string `toml:"busybox_sha256,omitempty"`
}

// FilesystemConfig defines filesystem options for oci_rootfs strategy.
type FilesystemConfig struct {
	Type         string `toml:"type"`
	SizeBufferMB int    `toml:"size_buffer_mb"`
	Preallocate  bool   `toml:"preallocate"`
}

// DefaultFilesystemConfig returns the default filesystem configuration.
func DefaultFilesystemConfig() *FilesystemConfig {
	return &FilesystemConfig{
		Type:         "ext4",
		SizeBufferMB: 100,
		Preallocate:  false,
	}
}

// DefaultAgentConfig returns the default agent configuration.
func DefaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		SourceStrategy: "release",
		Version:        "latest",
	}
}

// Constants for validation
const (
	StrategyOCIRootfs = "oci_rootfs"
	StrategyInitramfs = "initramfs"

	AgentSourceRelease = "release"
	AgentSourceLocal   = "local"
	AgentSourceHTTP    = "http"
)
