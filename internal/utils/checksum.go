package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/volantvm/fledge/internal/logging"
)

// VerifyChecksum verifies a file's SHA256 checksum.
// The expectedChecksum should be in the format "sha256:hash" or just "hash".
func VerifyChecksum(filePath, expectedChecksum string) error {
	if expectedChecksum == "" {
		logging.Warn("No checksum provided, skipping verification", "file", filePath)
		return nil
	}

	// Parse checksum format (support both "sha256:hash" and plain "hash")
	expectedHash := strings.TrimPrefix(expectedChecksum, "sha256:")
	expectedHash = strings.ToLower(strings.TrimSpace(expectedHash))

	// Calculate actual checksum
	actualHash, err := CalculateSHA256(filePath)
	if err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	// Compare
	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch:\n  expected: %s\n  got:      %s", expectedHash, actualHash)
	}

	logging.Debug("Checksum verification passed", "file", filePath, "hash", actualHash)
	return nil
}

// CalculateSHA256 calculates the SHA256 hash of a file.
func CalculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
