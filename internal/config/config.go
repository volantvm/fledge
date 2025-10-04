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

	// Apply default filesystem config for oci_rootfs if not provided
	if cfg.Strategy == StrategyOCIRootfs && cfg.Filesystem == nil {
		cfg.Filesystem = DefaultFilesystemConfig()
	} else if cfg.Strategy == StrategyOCIRootfs && cfg.Filesystem != nil {
		// Fill in missing fields with defaults
		defaults := DefaultFilesystemConfig()
		if cfg.Filesystem.Type == "" {
			cfg.Filesystem.Type = defaults.Type
		}
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
	if cfg.Source.Image == "" {
		return fmt.Errorf("'source.image' is required for oci_rootfs strategy")
	}

	if cfg.Filesystem == nil {
		return fmt.Errorf("'filesystem' section is required for oci_rootfs strategy")
	}

	// Validate filesystem type
	validFsTypes := map[string]bool{
		"ext4":  true,
		"xfs":   true,
		"btrfs": true,
	}
	if !validFsTypes[cfg.Filesystem.Type] {
		return fmt.Errorf("invalid filesystem type '%s', must be one of: ext4, xfs, btrfs",
			cfg.Filesystem.Type)
	}

	if cfg.Filesystem.SizeBufferMB < 0 {
		return fmt.Errorf("filesystem.size_buffer_mb must be non-negative, got %d",
			cfg.Filesystem.SizeBufferMB)
	}

	return nil
}

// validateInitramfs validates configuration for initramfs strategy.
func validateInitramfs(cfg *Config) error {
	if cfg.Source.BusyboxURL == "" {
		return fmt.Errorf("'source.busybox_url' is required for initramfs strategy")
	}

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
		// Path must be absolute
		if !filepath.IsAbs(cfg.Init.Path) {
			return fmt.Errorf("[init] path must be absolute (start with /), got: %s", cfg.Init.Path)
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
