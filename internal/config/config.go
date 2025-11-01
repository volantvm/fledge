package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load reads and parses a fledge.toml configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse TOML: %w", err)
	}

	// Apply defaults
	if err := applyDefaults(&cfg); err != nil {
		return nil, fmt.Errorf("failed to apply defaults: %w", err)
	}

	// Validate
	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return &cfg, nil
}

// LoadManifestTemplate reads and parses a manifest.toml template file.
// This file defines runtime defaults that can be overridden at VM creation time.
func LoadManifestTemplate(path string) (*ManifestTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest template %s: %w", path, err)
	}

	var tpl ManifestTemplate
	if err := toml.Unmarshal(data, &tpl); err != nil {
		return nil, fmt.Errorf("failed to parse manifest TOML: %w", err)
	}

	// Apply defaults for missing fields
	if err := applyManifestDefaults(&tpl); err != nil {
		return nil, fmt.Errorf("failed to apply manifest defaults: %w", err)
	}

	// Validate manifest template
	if err := ValidateManifestTemplate(&tpl); err != nil {
		return nil, fmt.Errorf("manifest validation failed: %w", err)
	}

	return &tpl, nil
}

// applyManifestDefaults applies default values to the manifest template.
func applyManifestDefaults(tpl *ManifestTemplate) error {
	// Default schema version
	if tpl.SchemaVersion == "" {
		tpl.SchemaVersion = "v1"
	}

	// Default resources if not specified
	if tpl.Resources == nil {
		tpl.Resources = &ResourcesConfig{
			CPUCores: 1,
			MemoryMB: 256,
		}
	}

	// Default network mode
	if tpl.Network != nil && tpl.Network.Mode == "" {
		tpl.Network.Mode = "bridged"
	}

	// Default protocol for port mappings
	if tpl.Network != nil {
		for i := range tpl.Network.Expose {
			if tpl.Network.Expose[i].Protocol == "" {
				tpl.Network.Expose[i].Protocol = "tcp"
			}
		}
	}

	return nil
}

// ValidateManifestTemplate validates a manifest template.
func ValidateManifestTemplate(tpl *ManifestTemplate) error {
	if tpl.SchemaVersion == "" {
		return fmt.Errorf("schema_version is required")
	}

	if tpl.SchemaVersion != "v1" {
		return fmt.Errorf("unsupported schema_version %q (expected \"v1\")", tpl.SchemaVersion)
	}

	if tpl.Name == "" {
		return fmt.Errorf("name is required")
	}

	if tpl.Version == "" {
		return fmt.Errorf("version is required")
	}

	if tpl.Runtime == "" {
		return fmt.Errorf("runtime is required")
	}

	// Validate resources if specified
	if tpl.Resources != nil {
		if tpl.Resources.CPUCores < 1 {
			return fmt.Errorf("resources.cpu_cores must be >= 1")
		}
		if tpl.Resources.MemoryMB < 128 {
			return fmt.Errorf("resources.memory_mb must be >= 128")
		}
	}

	// Validate network mode if specified
	if tpl.Network != nil {
		validModes := map[string]bool{"bridged": true, "vsock": true, "dhcp": true}
		if !validModes[tpl.Network.Mode] {
			return fmt.Errorf("invalid network.mode %q (must be bridged, vsock, or dhcp)", tpl.Network.Mode)
		}

		// Validate port mappings
		for i, port := range tpl.Network.Expose {
			if port.Port < 1 || port.Port > 65535 {
				return fmt.Errorf("network.expose[%d].port must be 1-65535", i)
			}
			if port.Protocol != "tcp" && port.Protocol != "udp" && port.Protocol != "" {
				return fmt.Errorf("network.expose[%d].protocol must be tcp or udp", i)
			}
		}
	}

	return nil
}

// applyDefaults applies default values for optional fields.
func applyDefaults(cfg *Config) error {
	// Apply default agent config for initramfs if not provided
	// Only apply default agent in "default" init mode, not for custom or none modes
	if cfg.Strategy == StrategyInitramfs && cfg.Agent == nil {
		initMode := getInitMode(cfg)
		if initMode == "default" {
			cfg.Agent = DefaultAgentConfig()
		}
	}

	// Initramfs: provide default Busybox if not specified
	if cfg.Strategy == StrategyInitramfs {
		if cfg.Source.BusyboxURL == "" {
			cfg.Source.BusyboxURL = DefaultBusyboxURL
		}
		if cfg.Source.BusyboxSHA256 == "" {
			cfg.Source.BusyboxSHA256 = DefaultBusyboxSHA256
		}
	}

	// Apply default filesystem config for oci_rootfs if not provided
	if cfg.Strategy == StrategyOCIRootfs && cfg.Filesystem == nil {
		cfg.Filesystem = DefaultFilesystemConfig()
	} else if cfg.Strategy == StrategyOCIRootfs && cfg.Filesystem != nil {
		// Fill in missing fields with defaults
		defaults := DefaultFilesystemConfig()
		if cfg.Filesystem.Type == "" {
			cfg.Filesystem.Type = defaults.Type
		}
		// Apply squashfs defaults if using squashfs
		if cfg.Filesystem.Type == "squashfs" {
			if cfg.Filesystem.CompressionLevel == 0 {
				cfg.Filesystem.CompressionLevel = defaults.CompressionLevel
			}
			if cfg.Filesystem.OverlaySize == "" {
				cfg.Filesystem.OverlaySize = defaults.OverlaySize
			}
		}
		// Apply legacy ext4/xfs/btrfs defaults
		if cfg.Filesystem.SizeBufferMB == 0 {
			cfg.Filesystem.SizeBufferMB = defaults.SizeBufferMB
		}
	}

	return nil
}

// Validate validates the configuration for correctness and completeness.
func Validate(cfg *Config) error {
	// Check version
	if cfg.Version == "" {
		return fmt.Errorf("'version' field is required")
	}
	if cfg.Version != "1" {
		return fmt.Errorf("unsupported config version '%s', expected '1'", cfg.Version)
	}

	// Check strategy
	if cfg.Strategy == "" {
		return fmt.Errorf("'strategy' field is required")
	}
	if cfg.Strategy != StrategyOCIRootfs && cfg.Strategy != StrategyInitramfs {
		return fmt.Errorf("invalid strategy '%s', must be '%s' or '%s'",
			cfg.Strategy, StrategyOCIRootfs, StrategyInitramfs)
	}

	// Strategy-specific validation
	switch cfg.Strategy {
	case StrategyOCIRootfs:
		if err := validateOCIRootfs(cfg); err != nil {
			return err
		}
	case StrategyInitramfs:
		if err := validateInitramfs(cfg); err != nil {
			return err
		}
	}

	// Validate mappings
	if err := validateMappings(cfg.Mappings); err != nil {
		return err
	}

	return nil
}

// validateOCIRootfs validates configuration for oci_rootfs strategy.
func validateOCIRootfs(cfg *Config) error {
	// Allow either an existing image reference OR a Dockerfile build input
	if cfg.Source.Image == "" && cfg.Source.Dockerfile == "" {
		return fmt.Errorf("either 'source.image' or 'source.dockerfile' is required for oci_rootfs strategy")
	}
	if cfg.Source.Image != "" && cfg.Source.Dockerfile != "" {
		return fmt.Errorf("only one of 'source.image' or 'source.dockerfile' may be specified for oci_rootfs strategy")
	}

	if cfg.Filesystem == nil {
		return fmt.Errorf("'filesystem' section is required for oci_rootfs strategy")
	}

	// Validate filesystem type
	validFsTypes := map[string]bool{
		"squashfs": true,
		"ext4":     true, // legacy
		"xfs":      true, // legacy
		"btrfs":    true, // legacy
	}
	if !validFsTypes[cfg.Filesystem.Type] {
		return fmt.Errorf("invalid filesystem type '%s', must be one of: squashfs (recommended), ext4, xfs, btrfs",
			cfg.Filesystem.Type)
	}
	
	// Validate squashfs-specific options
	if cfg.Filesystem.Type == "squashfs" {
		if cfg.Filesystem.CompressionLevel < 0 || cfg.Filesystem.CompressionLevel > 22 {
			return fmt.Errorf("squashfs compression_level must be between 0-22, got %d", cfg.Filesystem.CompressionLevel)
		}
		if cfg.Filesystem.OverlaySize == "" {
			return fmt.Errorf("squashfs overlay_size is required")
		}
	}

	if cfg.Filesystem.SizeBufferMB < 0 {
		return fmt.Errorf("filesystem.size_buffer_mb must be non-negative, got %d",
			cfg.Filesystem.SizeBufferMB)
	}

	return nil
}

// validateInitramfs validates configuration for initramfs strategy.
func validateInitramfs(cfg *Config) error {
	// Busybox URL is optional; defaults are applied in applyDefaults

	// Validate init configuration
	if err := validateInitConfig(cfg); err != nil {
		return err
	}

	// Agent validation depends on init mode
	initMode := getInitMode(cfg)

	switch initMode {
	case "default":
		// Default mode requires agent
		if cfg.Agent == nil {
			return fmt.Errorf("'agent' section is required for default init mode (no [init] section)")
		}
		return validateAgentConfig(cfg.Agent)

	case "custom":
		// Custom init mode - agent not allowed
		if cfg.Agent != nil {
			return fmt.Errorf("'agent' section cannot be specified with custom init mode ([init] path set)")
		}

	case "none":
		// None mode - agent not allowed
		if cfg.Agent != nil {
			return fmt.Errorf("'agent' section cannot be specified with no-init mode ([init] none=true)")
		}
	}

	return nil
}

// getInitMode determines the init mode from the config.
func getInitMode(cfg *Config) string {
	if cfg.Init == nil {
		return "default"
	}
	if cfg.Init.None {
		return "none"
	}
	if cfg.Init.Path != "" {
		return "custom"
	}
	return "default"
}

// validateInitConfig validates the [init] section.
func validateInitConfig(cfg *Config) error {
	if cfg.Init == nil {
		return nil // Default mode is valid
	}

	// Validate none and path are mutually exclusive
	if cfg.Init.None && cfg.Init.Path != "" {
		return fmt.Errorf("[init] cannot specify both none=true and path")
	}

	// Validate custom init path
	if cfg.Init.Path != "" {
		// Path can be relative (to config file) or absolute
		if cfg.Init.Path == "" {
			return fmt.Errorf("[init] path cannot be empty")
		}
	}

	return nil
}

// validateAgentConfig validates the agent configuration.
func validateAgentConfig(agent *AgentConfig) error {
	if agent.SourceStrategy == "" {
		return fmt.Errorf("'agent.source_strategy' is required")
	}

	switch agent.SourceStrategy {
	case AgentSourceRelease:
		if agent.Version == "" {
			return fmt.Errorf("'agent.version' is required when using 'release' source strategy")
		}
	case AgentSourceLocal:
		if agent.Path == "" {
			return fmt.Errorf("'agent.path' is required when using 'local' source strategy")
		}
		// Validate path exists (will be checked at build time, but we can give early feedback)
	case AgentSourceHTTP:
		if agent.URL == "" {
			return fmt.Errorf("'agent.url' is required when using 'http' source strategy")
		}
		// Checksum is optional but recommended
	default:
		return fmt.Errorf("invalid agent.source_strategy '%s', must be one of: release, local, http",
			agent.SourceStrategy)
	}

	return nil
}

// validateMappings validates file mappings.
func validateMappings(mappings map[string]string) error {
	for src, dst := range mappings {
		// Source path validation
		if src == "" {
			return fmt.Errorf("mapping source path cannot be empty")
		}

		// Destination path validation
		if dst == "" {
			return fmt.Errorf("mapping destination path cannot be empty for source '%s'", src)
		}

		if !filepath.IsAbs(dst) {
			return fmt.Errorf("mapping destination '%s' must be an absolute path (start with /)", dst)
		}

		// Warn about common issues
		if strings.Contains(dst, "..") {
			return fmt.Errorf("mapping destination '%s' contains '..' which is not allowed", dst)
		}
	}

	return nil
}
