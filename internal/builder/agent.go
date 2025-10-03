// Package builder provides the core build logic for Fledge.
package builder

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/volantvm/fledge/internal/config"
	"github.com/volantvm/fledge/internal/logging"
	"github.com/volantvm/fledge/internal/utils"
)

const (
	// DefaultGitHubRepo is the default GitHub repository for Volant releases.
	DefaultGitHubRepo = "volantvm/volant"
	// DefaultAgentBinaryName is the name of the kestrel agent binary.
	DefaultAgentBinaryName = "kestrel"
)

// GitHubRelease represents a GitHub release response.
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// SourceAgent sources the kestrel agent binary based on the configuration.
// Returns the path to the agent binary.
func SourceAgent(agentCfg *config.AgentConfig, showProgress bool) (string, error) {
	if agentCfg == nil {
		return "", fmt.Errorf("agent configuration is nil")
	}

	logging.Info("Sourcing agent", "strategy", agentCfg.SourceStrategy)

	switch agentCfg.SourceStrategy {
	case config.AgentSourceRelease:
		return sourceAgentFromRelease(agentCfg.Version, showProgress)
	case config.AgentSourceLocal:
		return sourceAgentFromLocal(agentCfg.Path)
	case config.AgentSourceHTTP:
		return sourceAgentFromHTTP(agentCfg.URL, agentCfg.Checksum, showProgress)
	default:
		return "", fmt.Errorf("unknown agent source strategy: %s", agentCfg.SourceStrategy)
	}
}

// sourceAgentFromRelease fetches the kestrel binary from GitHub releases.
func sourceAgentFromRelease(version string, showProgress bool) (string, error) {
	logging.Info("Fetching agent from GitHub releases", "version", version)

	// Fetch release information from GitHub API
	var releaseURL string
	if version == "latest" {
		releaseURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", DefaultGitHubRepo)
	} else {
		releaseURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", DefaultGitHubRepo, version)
	}

	logging.Debug("Fetching release info", "url", releaseURL)

	resp, err := http.Get(releaseURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to parse release JSON: %w", err)
	}

	// Find the kestrel asset
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == DefaultAgentBinaryName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return "", fmt.Errorf("kestrel binary not found in release %s", release.TagName)
	}

	logging.Info("Downloading kestrel", "version", release.TagName, "url", downloadURL)

	// Download to temp file
	tmpPath, err := utils.DownloadToTempFile(downloadURL, showProgress)
	if err != nil {
		return "", fmt.Errorf("failed to download kestrel: %w", err)
	}

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to make kestrel executable: %w", err)
	}

	logging.Info("Agent sourced successfully", "path", tmpPath, "version", release.TagName)
	return tmpPath, nil
}

// sourceAgentFromLocal copies the kestrel binary from a local path.
func sourceAgentFromLocal(localPath string) (string, error) {
	logging.Info("Sourcing agent from local path", "path", localPath)

	// Validate path exists
	if _, err := os.Stat(localPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("agent path does not exist: %s", localPath)
		}
		return "", fmt.Errorf("failed to access agent path: %w", err)
	}

	// Check if it's a file
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat agent file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("agent path is a directory, expected a file: %s", localPath)
	}

	// Create a temp copy to maintain consistency with other strategies
	tmpFile, err := os.CreateTemp("", "fledge-agent-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Copy file
	src, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open source agent: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to create destination: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to copy agent: %w", err)
	}

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to make agent executable: %w", err)
	}

	logging.Info("Agent sourced successfully from local path", "path", tmpPath)
	return tmpPath, nil
}

// sourceAgentFromHTTP downloads the kestrel binary from a custom HTTP URL.
func sourceAgentFromHTTP(url, checksum string, showProgress bool) (string, error) {
	logging.Info("Downloading agent from HTTP", "url", url)

	// Download to temp file
	tmpPath, err := utils.DownloadToTempFile(url, showProgress)
	if err != nil {
		return "", fmt.Errorf("failed to download agent: %w", err)
	}

	// Verify checksum if provided
	if checksum != "" {
		logging.Info("Verifying agent checksum")
		if err := utils.VerifyChecksum(tmpPath, checksum); err != nil {
			os.Remove(tmpPath)
			return "", fmt.Errorf("agent checksum verification failed: %w", err)
		}
	}

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to make agent executable: %w", err)
	}

	logging.Info("Agent sourced successfully from HTTP", "path", tmpPath)
	return tmpPath, nil
}

// CleanupAgent removes a temporary agent file.
func CleanupAgent(agentPath string) {
	if agentPath != "" {
		// Check if it's in a temp directory (handles symlinks on macOS)
		dir := filepath.Dir(agentPath)
		tmpDir := os.TempDir()

		// Resolve symlinks for comparison
		resolvedDir, _ := filepath.EvalSymlinks(dir)
		resolvedTmpDir, _ := filepath.EvalSymlinks(tmpDir)

		// Clean up if in temp directory or if the path contains temp patterns
		isTempFile := (resolvedDir == resolvedTmpDir) ||
			(dir == tmpDir) ||
			(resolvedDir != "" && resolvedTmpDir != "" && resolvedDir == resolvedTmpDir)

		if isTempFile {
			if err := os.Remove(agentPath); err != nil {
				logging.Warn("Failed to cleanup agent file", "path", agentPath, "error", err)
			} else {
				logging.Debug("Cleaned up agent file", "path", agentPath)
			}
		}
	}
}
