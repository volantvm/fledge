package builder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/volantvm/fledge/internal/config"
)

// TestSourceAgent_LocalStrategy tests the local sourcing strategy.
func TestSourceAgent_LocalStrategy(t *testing.T) {
	// Create a temporary agent file
	tmpDir := t.TempDir()
	agentPath := filepath.Join(tmpDir, "test-kestrel")

	// Write a dummy agent file
	content := []byte("#!/bin/sh\necho 'test agent'\n")
	if err := os.WriteFile(agentPath, content, 0755); err != nil {
		t.Fatalf("Failed to create test agent: %v", err)
	}

	// Test local sourcing
	agentCfg := &config.AgentConfig{
		SourceStrategy: config.AgentSourceLocal,
		Path:           agentPath,
	}

	resultPath, err := SourceAgent(agentCfg, false)
	if err != nil {
		t.Fatalf("SourceAgent failed: %v", err)
	}
	defer CleanupAgent(resultPath)

	// Verify the result path exists
	if _, err := os.Stat(resultPath); err != nil {
		t.Errorf("Result agent file does not exist: %v", err)
	}

	// Verify it's executable
	info, err := os.Stat(resultPath)
	if err != nil {
		t.Fatalf("Failed to stat result agent: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Error("Result agent is not executable")
	}

	// Verify content matches
	resultContent, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("Failed to read result agent: %v", err)
	}
	if string(resultContent) != string(content) {
		t.Error("Result agent content does not match source")
	}
}

// TestSourceAgent_LocalStrategy_NonExistent tests error handling for non-existent local files.
func TestSourceAgent_LocalStrategy_NonExistent(t *testing.T) {
	agentCfg := &config.AgentConfig{
		SourceStrategy: config.AgentSourceLocal,
		Path:           "/nonexistent/path/to/agent",
	}

	_, err := SourceAgent(agentCfg, false)
	if err == nil {
		t.Fatal("Expected error for non-existent path, got nil")
	}
}

// TestSourceAgent_LocalStrategy_Directory tests error handling when path is a directory.
func TestSourceAgent_LocalStrategy_Directory(t *testing.T) {
	tmpDir := t.TempDir()

	agentCfg := &config.AgentConfig{
		SourceStrategy: config.AgentSourceLocal,
		Path:           tmpDir,
	}

	_, err := SourceAgent(agentCfg, false)
	if err == nil {
		t.Fatal("Expected error for directory path, got nil")
	}
}

// TestSourceAgent_NilConfig tests error handling for nil configuration.
func TestSourceAgent_NilConfig(t *testing.T) {
	_, err := SourceAgent(nil, false)
	if err == nil {
		t.Fatal("Expected error for nil config, got nil")
	}
}

// TestSourceAgent_UnknownStrategy tests error handling for unknown strategy.
func TestSourceAgent_UnknownStrategy(t *testing.T) {
	agentCfg := &config.AgentConfig{
		SourceStrategy: "invalid_strategy",
	}

	_, err := SourceAgent(agentCfg, false)
	if err == nil {
		t.Fatal("Expected error for unknown strategy, got nil")
	}
}

// TestCleanupAgent tests cleanup of temporary agent files.
func TestCleanupAgent(t *testing.T) {
	// Create a temp file
	tmpFile, err := os.CreateTemp("", "test-agent-*")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Verify it exists
	if _, err := os.Stat(tmpPath); err != nil {
		t.Fatalf("Temp file doesn't exist: %v", err)
	}

	// Cleanup
	CleanupAgent(tmpPath)

	// Verify it's gone
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("Temp file still exists after cleanup")
	}
}

// TestCleanupAgent_NonTempFile tests that non-temp files are not cleaned up.
func TestCleanupAgent_NonTempFile(t *testing.T) {
	// Create a file in a non-temp location
	tmpDir := t.TempDir()
	nonTempPath := filepath.Join(tmpDir, "agent")

	if err := os.WriteFile(nonTempPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Try to cleanup (should not remove it)
	CleanupAgent(nonTempPath)

	// Verify it still exists
	if _, err := os.Stat(nonTempPath); err != nil {
		t.Error("Non-temp file was incorrectly removed")
	}
}

// Note: Tests for HTTP and GitHub release strategies would require either:
// 1. Network access (not ideal for unit tests)
// 2. HTTP mock servers (more complex setup)
// 3. Integration tests (run separately)
// For now, we focus on the local strategy which doesn't require network access.
// The HTTP strategies are tested implicitly through manual testing and E2E tests.
