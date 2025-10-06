package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadValidInitramfs tests loading a valid initramfs configuration.
func TestLoadValidInitramfs(t *testing.T) {
	content := `
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "release"
version = "latest"

[source]
busybox_url = "https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
busybox_sha256 = "6e123e7f3202a8c1e9b1f94d8941580a25135382b99e8d3e34fb858bba311348"

[mappings]
"payload/my-app" = "/usr/bin/my-app"
"payload/config.yml" = "/etc/my-app/config.yml"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Version != "1" {
		t.Errorf("expected version '1', got '%s'", cfg.Version)
	}
	if cfg.Strategy != StrategyInitramfs {
		t.Errorf("expected strategy '%s', got '%s'", StrategyInitramfs, cfg.Strategy)
	}
	if cfg.Agent.SourceStrategy != "release" {
		t.Errorf("expected agent source_strategy 'release', got '%s'", cfg.Agent.SourceStrategy)
	}
	if len(cfg.Mappings) != 2 {
		t.Errorf("expected 2 mappings, got %d", len(cfg.Mappings))
	}
}

// TestLoadValidOCIRootfs tests loading a valid oci_rootfs configuration.
func TestLoadValidOCIRootfs(t *testing.T) {
	content := `
version = "1"
strategy = "oci_rootfs"

[source]
image = "docker.io/library/nginx:alpine"

[filesystem]
type = "ext4"
size_buffer_mb = 100

[mappings]
"payload/nginx.conf" = "/etc/nginx/nginx.conf"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Strategy != StrategyOCIRootfs {
		t.Errorf("expected strategy '%s', got '%s'", StrategyOCIRootfs, cfg.Strategy)
	}
	if cfg.Source.Image != "docker.io/library/nginx:alpine" {
		t.Errorf("unexpected image: %s", cfg.Source.Image)
	}
	if cfg.Filesystem.Type != "ext4" {
		t.Errorf("expected filesystem type 'ext4', got '%s'", cfg.Filesystem.Type)
	}
}

// TestLoadWithDefaults tests that defaults are applied correctly.
func TestLoadWithDefaults(t *testing.T) {
	// Minimal initramfs config - agent defaults should be applied
	content := `
version = "1"
strategy = "initramfs"

[source]
busybox_url = "https://busybox.net/test/busybox"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Agent == nil {
		t.Fatal("expected agent config to be populated with defaults")
	}
	if cfg.Agent.SourceStrategy != "release" {
		t.Errorf("expected default agent source_strategy 'release', got '%s'", cfg.Agent.SourceStrategy)
	}
	if cfg.Agent.Version != "latest" {
		t.Errorf("expected default agent version 'latest', got '%s'", cfg.Agent.Version)
	}
}

// TestLoadOCIRootfsWithDefaults tests OCI defaults.
func TestLoadOCIRootfsWithDefaults(t *testing.T) {
	content := `
version = "1"
strategy = "oci_rootfs"

[source]
image = "nginx:alpine"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Filesystem == nil {
		t.Fatal("expected filesystem config to be populated with defaults")
	}
	if cfg.Filesystem.Type != "ext4" {
		t.Errorf("expected default filesystem type 'ext4', got '%s'", cfg.Filesystem.Type)
	}
	if cfg.Filesystem.SizeBufferMB != 0 {
		t.Errorf("expected default size_buffer_mb 0 (auto), got %d", cfg.Filesystem.SizeBufferMB)
	}
}

// TestValidationMissingVersion tests error when version is missing.
func TestValidationMissingVersion(t *testing.T) {
	content := `
strategy = "initramfs"

[source]
busybox_url = "https://test.com/busybox"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error for missing version, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention 'version', got: %v", err)
	}
}

// TestValidationInvalidStrategy tests error for invalid strategy.
func TestValidationInvalidStrategy(t *testing.T) {
	content := `
version = "1"
strategy = "invalid_strategy"

[source]
image = "test"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error for invalid strategy, got nil")
	}
	if !strings.Contains(err.Error(), "invalid strategy") {
		t.Errorf("error should mention 'invalid strategy', got: %v", err)
	}
}

// TestValidationOCIRootfsMissingImage tests OCI validation.
func TestValidationOCIRootfsMissingImage(t *testing.T) {
	content := `
version = "1"
strategy = "oci_rootfs"

[filesystem]
type = "ext4"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error when neither image nor dockerfile provided, got nil")
	}
	if !strings.Contains(err.Error(), "dockerfile") && !strings.Contains(err.Error(), "source.image") {
		t.Errorf("error should mention 'source.image' or 'dockerfile', got: %v", err)
	}
}

// TestValidationInvalidFilesystemType tests invalid filesystem type.
func TestValidationInvalidFilesystemType(t *testing.T) {
	content := `
version = "1"
strategy = "oci_rootfs"

[source]
image = "nginx:alpine"

[filesystem]
type = "ntfs"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error for invalid filesystem type, got nil")
	}
	if !strings.Contains(err.Error(), "invalid filesystem type") {
		t.Errorf("error should mention 'invalid filesystem type', got: %v", err)
	}
}

// TestValidationInitramfsMissingBusybox tests initramfs validation.
func TestInitramfsDefaultsBusyboxApplied(t *testing.T) {
	content := `
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "release"
version = "latest"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source.BusyboxURL == "" {
		t.Fatalf("expected default busybox_url to be applied")
	}
}

// TestValidationAgentLocalMissingPath tests agent local validation.
func TestValidationAgentLocalMissingPath(t *testing.T) {
	content := `
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "local"

[source]
busybox_url = "https://test.com/busybox"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error for missing agent path, got nil")
	}
	if !strings.Contains(err.Error(), "agent.path") {
		t.Errorf("error should mention 'agent.path', got: %v", err)
	}
}

// TestValidationAgentHTTPMissingURL tests agent http validation.
func TestValidationAgentHTTPMissingURL(t *testing.T) {
	content := `
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "http"

[source]
busybox_url = "https://test.com/busybox"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error for missing agent URL, got nil")
	}
	if !strings.Contains(err.Error(), "agent.url") {
		t.Errorf("error should mention 'agent.url', got: %v", err)
	}
}

// TestValidationMappingsRelativePath tests mapping validation.
func TestValidationMappingsRelativePath(t *testing.T) {
	content := `
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "release"
version = "latest"

[source]
busybox_url = "https://test.com/busybox"

[mappings]
"payload/app" = "usr/bin/app"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error for relative mapping destination, got nil")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("error should mention 'absolute path', got: %v", err)
	}
}

// TestValidationMappingsWithDotDot tests that .. in paths is rejected.
func TestValidationMappingsWithDotDot(t *testing.T) {
	content := `
version = "1"
strategy = "initramfs"

[agent]
source_strategy = "release"
version = "latest"

[source]
busybox_url = "https://test.com/busybox"

[mappings]
"payload/app" = "/usr/../etc/app"
`

	tmpFile := writeTempConfig(t, content)
	defer os.Remove(tmpFile)

	_, err := Load(tmpFile)
	if err == nil {
		t.Fatal("expected error for .. in mapping destination, got nil")
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("error should mention '..', got: %v", err)
	}
}

// writeTempConfig writes a temporary config file for testing.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "fledge.toml")

	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	return tmpFile
}
