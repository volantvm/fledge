package builder

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/volantvm/fledge/internal/logging"
)

// FileMapping represents a source-to-destination file mapping.
type FileMapping struct {
	Source      string      // Source path (relative to working directory)
	Destination string      // Destination path (absolute path in artifact)
	IsDirectory bool        // Whether the source is a directory
	Mode        os.FileMode // File permissions
}

// FHS executable paths that should have execute permissions
var fhsExecutablePaths = []string{
	"/bin/",
	"/sbin/",
	"/usr/bin/",
	"/usr/sbin/",
	"/usr/local/bin/",
	"/usr/local/sbin/",
	"/opt/bin/",
	"/opt/sbin/",
}

// FHS library paths that should have execute permissions
var fhsLibraryPaths = []string{
	"/lib/",
	"/lib64/",
	"/usr/lib/",
	"/usr/lib64/",
	"/usr/local/lib/",
	"/usr/local/lib64/",
}

// PrepareFileMappings prepares and validates file mappings from the config.
// It resolves source paths, determines file types, and assigns appropriate permissions.
func PrepareFileMappings(mappings map[string]string, workDir string) ([]FileMapping, error) {
	if len(mappings) == 0 {
		logging.Warn("No file mappings provided")
		return []FileMapping{}, nil
	}

	logging.Info("Preparing file mappings", "count", len(mappings))

	var result []FileMapping
	for src, dst := range mappings {
		// Resolve source path relative to working directory
		srcPath := src
		if !filepath.IsAbs(src) {
			srcPath = filepath.Join(workDir, src)
		}

		// Validate source exists
		info, err := os.Stat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("source file does not exist: %s", src)
			}
			return nil, fmt.Errorf("failed to stat source %s: %w", src, err)
		}

		// Determine permissions based on destination path and file type
		mode := DetermineFileMode(dst, info)

		mapping := FileMapping{
			Source:      srcPath,
			Destination: dst,
			IsDirectory: info.IsDir(),
			Mode:        mode,
		}

		result = append(result, mapping)
		logging.Debug("Mapped file",
			"source", src,
			"destination", dst,
			"mode", fmt.Sprintf("%04o", mode),
			"is_dir", mapping.IsDirectory)
	}

	logging.Info("File mappings prepared", "total", len(result))
	return result, nil
}

// DetermineFileMode determines the appropriate file mode based on the destination path
// and original file info, following FHS conventions.
func DetermineFileMode(destPath string, info os.FileInfo) os.FileMode {
	// Start with the original file mode
	baseMode := info.Mode()

	// If it's a directory, ensure it has execute permissions
	if info.IsDir() {
		return 0755
	}

	// Check if file already has any execute permission
	if baseMode&0111 != 0 {
		// Already executable, preserve but normalize to common patterns
		return normalizeExecutableMode(baseMode)
	}

	// Check if the destination is in an FHS executable path
	if isInFHSExecutablePath(destPath) {
		logging.Debug("Adding execute permission for FHS executable path", "path", destPath)
		return 0755
	}

	// Check if the destination is in an FHS library path
	if isInFHSLibraryPath(destPath) {
		// Libraries should be readable and executable (for dynamic linking)
		logging.Debug("Adding execute permission for FHS library path", "path", destPath)
		return 0755
	}

	// For all other files, use read/write for owner, read for group/other
	return 0644
}

// isInFHSExecutablePath checks if a path is within a standard FHS executable directory.
func isInFHSExecutablePath(path string) bool {
	for _, prefix := range fhsExecutablePaths {
		if strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

// isInFHSLibraryPath checks if a path is within a standard FHS library directory.
func isInFHSLibraryPath(path string) bool {
	for _, prefix := range fhsLibraryPaths {
		if strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/") {
			// Additional check for library file extensions
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".so" || strings.Contains(path, ".so.") {
				return true
			}
		}
	}
	return false
}

// normalizeExecutableMode normalizes executable permissions to common patterns.
func normalizeExecutableMode(mode os.FileMode) os.FileMode {
	// If any execute bit is set, set all execute bits that correspond to read bits
	if mode&0111 != 0 {
		newMode := mode & 0666 // Start with read/write bits

		// If owner can read, owner can execute
		if mode&0400 != 0 {
			newMode |= 0100
		}
		// If group can read, group can execute
		if mode&0040 != 0 {
			newMode |= 0010
		}
		// If others can read, others can execute
		if mode&0004 != 0 {
			newMode |= 0001
		}

		return newMode
	}

	return mode
}

// CopyFile copies a single file from source to destination with the specified mode.
func CopyFile(src, dst string, mode os.FileMode) error {
	logging.Debug("Copying file", "src", src, "dst", dst, "mode", fmt.Sprintf("%04o", mode))

	// Create destination directory if needed
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source: %w", err)
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}
	defer dstFile.Close()

	// Copy contents
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	// Ensure permissions are set correctly
	if err := os.Chmod(dst, mode); err != nil {
		return fmt.Errorf("failed to set file mode: %w", err)
	}

	return nil
}

// CopyDirectory recursively copies a directory from source to destination.
func CopyDirectory(src, dst string, baseMode os.FileMode) error {
	logging.Debug("Copying directory", "src", src, "dst", dst)

	// Create the destination directory
	if err := os.MkdirAll(dst, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Read source directory contents
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	// Copy each entry
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			// Recursively copy subdirectories
			if err := CopyDirectory(srcPath, dstPath, baseMode); err != nil {
				return err
			}
		} else {
			// Get file info for mode detection
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("failed to get file info: %w", err)
			}

			// Determine mode based on destination path
			mode := DetermineFileMode(dstPath, info)

			// Copy file
			if err := CopyFile(srcPath, dstPath, mode); err != nil {
				return err
			}
		}
	}

	return nil
}

// ApplyFileMappings applies all file mappings to the target directory.
func ApplyFileMappings(mappings []FileMapping, targetDir string) error {
	if len(mappings) == 0 {
		logging.Info("No file mappings to apply")
		return nil
	}

	logging.Info("Applying file mappings", "count", len(mappings), "target", targetDir)

	for i, mapping := range mappings {
		dstPath := filepath.Join(targetDir, strings.TrimPrefix(mapping.Destination, "/"))

		if mapping.IsDirectory {
			if err := CopyDirectory(mapping.Source, dstPath, mapping.Mode); err != nil {
				return fmt.Errorf("failed to copy directory %s -> %s: %w",
					mapping.Source, mapping.Destination, err)
			}
		} else {
			if err := CopyFile(mapping.Source, dstPath, mapping.Mode); err != nil {
				return fmt.Errorf("failed to copy file %s -> %s: %w",
					mapping.Source, mapping.Destination, err)
			}
		}

		logging.Info("Applied mapping",
			"index", i+1,
			"total", len(mappings),
			"src", mapping.Source,
			"dst", mapping.Destination)
	}

	logging.Info("All file mappings applied successfully")
	return nil
}
