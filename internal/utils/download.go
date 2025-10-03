// Package utils provides utility functions for Fledge.
package utils

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/schollz/progressbar/v3"
	"github.com/volantvm/fledge/internal/logging"
)

// DownloadFile downloads a file from a URL to a destination path with progress indication.
func DownloadFile(url, destPath string, showProgress bool) error {
	logging.Debug("Downloading file", "url", url, "dest", destPath)

	// Create destination directory if it doesn't exist
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Create HTTP request
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d: %s", resp.StatusCode, resp.Status)
	}

	// Create destination file
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", destPath, err)
	}
	defer out.Close()

	// Download with progress bar if enabled and size is known
	if showProgress && resp.ContentLength > 0 {
		bar := progressbar.DefaultBytes(
			resp.ContentLength,
			fmt.Sprintf("Downloading %s", filepath.Base(destPath)),
		)
		_, err = io.Copy(io.MultiWriter(out, bar), resp.Body)
	} else {
		_, err = io.Copy(out, resp.Body)
	}

	if err != nil {
		return fmt.Errorf("failed to save file: %w", err)
	}

	logging.Debug("Download complete", "file", destPath)
	return nil
}

// DownloadToTempFile downloads a file to a temporary location and returns the path.
func DownloadToTempFile(url string, showProgress bool) (string, error) {
	tmpFile, err := os.CreateTemp("", "fledge-download-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	if err := DownloadFile(url, tmpPath, showProgress); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	return tmpPath, nil
}
