// Package config provides configuration parsing and validation for fledge.toml.
package config

// Config represents the complete fledge.toml configuration.
type Config struct {
	Version    string            `toml:"version"`
	Strategy   string            `toml:"strategy"`
	Agent      *AgentConfig      `toml:"agent,omitempty"`
	Init       *InitConfig       `toml:"init,omitempty"` // Init configuration (default, custom, or none)
	Source     SourceConfig      `toml:"source"`
	Filesystem *FilesystemConfig `toml:"filesystem,omitempty"`
	Mappings   map[string]string `toml:"mappings,omitempty"`
}

// InitConfig defines init/PID1 behavior for initramfs.
// Three modes:
// 1. Default (nil or empty): C init → Kestrel (batteries-included)
// 2. Custom (Path set): C init → your custom init script/binary
// 3. None (None=true): Your payload becomes PID 1 directly (no wrapper)
type InitConfig struct {
	Path string `toml:"path,omitempty"` // Path to custom init (mode 2)
	None bool   `toml:"none,omitempty"` // Skip init wrapper entirely (mode 3)
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

	// Optional Dockerfile build inputs (for both strategies)
	// If Dockerfile is provided, Fledge will build the image locally using the
	// Docker daemon, then export/overlay it depending on the strategy.
	Dockerfile string            `toml:"dockerfile,omitempty"`
	Context    string            `toml:"context,omitempty"`
	Target     string            `toml:"target,omitempty"`
	BuildArgs  map[string]string `toml:"build_args,omitempty"`

	// For "initramfs" strategy
	BusyboxURL    string `toml:"busybox_url,omitempty"`
	BusyboxSHA256 string `toml:"busybox_sha256,omitempty"`
}

// FilesystemConfig defines filesystem options for oci_rootfs strategy.
// Note: squashfs is the default and recommended format (read-only compressed rootfs with overlayfs).
// ext4/xfs/btrfs are legacy options retained for compatibility.
type FilesystemConfig struct {
	Type              string `toml:"type"`
	SizeBufferMB      int    `toml:"size_buffer_mb"`       // Only used for ext4/xfs/btrfs (legacy)
	Preallocate       bool   `toml:"preallocate"`           // Only used for ext4/xfs/btrfs (legacy)
	CompressionLevel  int    `toml:"compression_level"`    // Squashfs compression level (1-22, default 15)
	OverlaySize       string `toml:"overlay_size"`          // Overlay tmpfs size (e.g., "512M", "1G", "50%"), default "1G"
}

// DefaultFilesystemConfig returns the default filesystem configuration.
func DefaultFilesystemConfig() *FilesystemConfig {
	return &FilesystemConfig{
		Type:             "squashfs",
		CompressionLevel: 15,     // Balanced compression
		OverlaySize:      "1G",   // 1GB tmpfs for runtime writes
		// Legacy options (only used if Type is ext4/xfs/btrfs)
		SizeBufferMB: 0,
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

// Default Busybox (musl static) used when not provided by user.
// Users can override via [source] busybox_url and busybox_sha256.
const (
	DefaultBusyboxURL    = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
	DefaultBusyboxSHA256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"
)
