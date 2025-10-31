package builder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"
	"github.com/volantvm/fledge/internal/config"
	"github.com/volantvm/fledge/internal/logging"
)

// OCIIndex represents the OCI index.json structure
type OCIIndex struct {
	Manifests []OCIManifest `json:"manifests"`
}

// OCIManifest represents an OCI manifest entry
type OCIManifest struct {
	Config OCIDescriptor `json:"config"`
}

// OCIDescriptor represents an OCI descriptor with digest
type OCIDescriptor struct {
	Digest string `json:"digest"`
}

// OCIRootfsBuilder builds OCI rootfs filesystem images.
type OCIRootfsBuilder struct {
	Config         *config.Config
	WorkDir        string
	OutputPath     string
	TempDir        string
	OciLayoutPath  string
	UnpackedPath   string
	ImagePath      string
	MountPoint     string
	LoopDevicePath string
	EphemeralTag   string
	RootfsReady    bool
}

// NewOCIRootfsBuilder creates a new OCI rootfs builder.
func NewOCIRootfsBuilder(cfg *config.Config, workDir, outputPath string) *OCIRootfsBuilder {
	return &OCIRootfsBuilder{
		Config:     cfg,
		WorkDir:    workDir,
		OutputPath: outputPath,
	}
}

// Build creates the OCI rootfs filesystem image.
func (b *OCIRootfsBuilder) Build() error {
	// Adjust output extension based on filesystem type
	if b.Config.Filesystem.Type == "squashfs" && !strings.HasSuffix(b.OutputPath, ".squashfs") {
		// Replace .img with .squashfs if using squashfs
		if strings.HasSuffix(b.OutputPath, ".img") {
			b.OutputPath = strings.TrimSuffix(b.OutputPath, ".img") + ".squashfs"
		} else {
			b.OutputPath = b.OutputPath + ".squashfs"
		}
	}

	logging.Info("Building OCI rootfs", "output", b.OutputPath, "type", b.Config.Filesystem.Type)

	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "fledge-oci-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	defer b.cleanup()

	b.TempDir = tmpDir
	b.OciLayoutPath = filepath.Join(tmpDir, "oci-layout")
	b.UnpackedPath = filepath.Join(tmpDir, "unpacked-rootfs")

	// Use appropriate temp file extension
	tempExt := ".img"
	if b.Config.Filesystem.Type == "squashfs" {
		tempExt = ".squashfs"
	}
	b.ImagePath = filepath.Join(tmpDir, "fs-image"+tempExt)
	b.MountPoint = filepath.Join(tmpDir, "mnt")

	logging.Debug("Created temporary directories", "temp", tmpDir)

	// Create required directories
	for _, dir := range []string{b.OciLayoutPath, b.UnpackedPath, b.MountPoint} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Build steps differ based on filesystem type
	var steps []struct {
		name string
		fn   func() error
	}

	if b.Config.Filesystem.Type == "squashfs" {
		// Squashfs pipeline: Build rootfs → Install agent → Create squashfs
		steps = []struct {
			name string
			fn   func() error
		}{
			{"Build Dockerfile (if provided)", b.buildDockerfileIfNeeded},
			{"Download OCI image", b.downloadOCIImage},
			{"Unpack image layers", b.unpackOCIImage},
			{"Extract OCI config", b.extractOCIConfig},
			{"Install kestrel agent", b.installAgent},
			{"Apply file mappings", b.applyMappings},
			{"Create squashfs image", b.createSquashfs},
			{"Move to final location", b.moveToFinal},
		}
	} else {
		// Legacy ext4/xfs/btrfs pipeline: Build rootfs → Create image → Mount → Copy → Shrink
		steps = []struct {
			name string
			fn   func() error
		}{
			{"Build Dockerfile (if provided)", b.buildDockerfileIfNeeded},
			{"Download OCI image", b.downloadOCIImage},
			{"Unpack image layers", b.unpackOCIImage},
			{"Extract OCI config", b.extractOCIConfig},
			{"Install kestrel agent", b.installAgent},
			{"Apply file mappings", b.applyMappings},
			{"Calculate disk size", b.createImageFile},
			{"Create filesystem", b.createFilesystem},
			{"Mount image", b.mountImage},
			{"Copy rootfs to image", b.copyRootfsToImage},
			{"Unmount image", b.unmountImage},
			{"Shrink to optimal size", b.shrinkFilesystem},
			{"Move to final location", b.moveToFinal},
		}
	}

	for _, step := range steps {
		logging.Info(step.name)
		if err := step.fn(); err != nil {
			return fmt.Errorf("%s failed: %w", step.name, err)
		}
	}

	logging.Info("OCI rootfs build complete", "output", b.OutputPath)
	return nil
}

// downloadOCIImage downloads the OCI image using skopeo.
func (b *OCIRootfsBuilder) downloadOCIImage() error {
	imageRef := b.Config.Source.Image

	if b.RootfsReady {
		logging.Debug("Skipping OCI image download: rootfs built via BuildKit")
		return nil
	}
	// Try local Docker daemon first
	cmd := exec.Command("skopeo", "copy",
		fmt.Sprintf("docker-daemon:%s", imageRef),
		fmt.Sprintf("oci:%s:latest", b.OciLayoutPath))

	output, err := cmd.CombinedOutput()
	if err == nil {
		logging.Debug("Copied from local Docker daemon")
		return nil
	}

	logging.Debug("Local Docker daemon copy failed, trying remote registry",
		"error", string(output))

	// Try remote registry
	cmd = exec.Command("skopeo", "copy",
		fmt.Sprintf("docker://%s", imageRef),
		fmt.Sprintf("oci:%s:latest", b.OciLayoutPath))

	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("skopeo copy failed: %w\nOutput: %s", err, string(output))
	}

	logging.Debug("Copied from remote registry")
	return nil
}

// unpackOCIImage unpacks the OCI image layers using umoci.
func (b *OCIRootfsBuilder) unpackOCIImage() error {
	if b.RootfsReady {
		logging.Debug("Skipping OCI unpack: rootfs built via BuildKit")
		return nil
	}
	cmd := exec.Command("umoci", "unpack",
		"--image", fmt.Sprintf("%s:latest", b.OciLayoutPath),
		b.UnpackedPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("umoci unpack failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// extractOCIConfig extracts the OCI config and saves it to /etc/fsify-entrypoint.
func (b *OCIRootfsBuilder) extractOCIConfig() error {
	configPath := filepath.Join(b.OciLayoutPath, "blobs", "sha256")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logging.Debug("No config blobs found, skipping OCI config extraction")
		return nil
	}

	// Read index.json
	indexPath := filepath.Join(b.OciLayoutPath, "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		logging.Debug("Could not read index.json, skipping config extraction")
		return nil
	}

	// Parse JSON
	var index OCIIndex
	if err := json.Unmarshal(indexData, &index); err != nil {
		logging.Debug("Could not parse index.json, skipping config extraction")
		return nil
	}

	if len(index.Manifests) == 0 {
		logging.Debug("No manifests found in index.json")
		return nil
	}

	configDigest := index.Manifests[0].Config.Digest
	if configDigest == "" {
		logging.Debug("No config digest found")
		return nil
	}

	// Extract config file
	if strings.HasPrefix(configDigest, "sha256:") {
		configDigest = strings.TrimPrefix(configDigest, "sha256:")
		sourceConfig := filepath.Join(configPath, configDigest)

		if _, err := os.Stat(sourceConfig); err == nil {
			// Create /etc directory in unpacked rootfs
			rootfsPath := filepath.Join(b.UnpackedPath, "rootfs")
			etcDir := filepath.Join(rootfsPath, "etc")
			if err := os.MkdirAll(etcDir, 0755); err != nil {
				return fmt.Errorf("failed to create /etc directory: %w", err)
			}

			// Copy config to /etc/fsify-entrypoint
			entrypointFile := filepath.Join(etcDir, "fsify-entrypoint")
			if err := copyFile(sourceConfig, entrypointFile); err != nil {
				return fmt.Errorf("failed to copy OCI config: %w", err)
			}

			logging.Debug("OCI config saved to /etc/fsify-entrypoint")
		}
	}

	return nil
}

// installAgent installs the kestrel agent binary.
func (b *OCIRootfsBuilder) installAgent() error {
	logging.Info("Installing kestrel agent")

	// Source the agent
	agentPath, err := SourceAgent(b.Config.Agent, true)
	if err != nil {
		return fmt.Errorf("failed to source agent: %w", err)
	}
	defer CleanupAgent(agentPath)

	// Copy agent to /bin/kestrel in unpacked rootfs
	rootfsPath := filepath.Join(b.UnpackedPath, "rootfs")

	// Verify rootfs directory exists and is a directory
	if info, err := os.Stat(rootfsPath); err != nil {
		if os.IsNotExist(err) {
			if mkdirErr := os.MkdirAll(rootfsPath, 0755); mkdirErr != nil {
				return fmt.Errorf("rootfs directory does not exist and cannot be created: %w", mkdirErr)
			}
		} else {
			return fmt.Errorf("failed to stat rootfs directory: %w", err)
		}
	} else if !info.IsDir() {
		return fmt.Errorf("rootfs path exists but is not a directory: %s", rootfsPath)
	}

	kestrelPath := filepath.Join(rootfsPath, "bin", "kestrel")
	binDir := filepath.Dir(kestrelPath)

	if err := ensureDestDir(rootfsPath, binDir); err != nil {
		return err
	}

	if err := CopyFile(agentPath, kestrelPath, 0755); err != nil {
		return fmt.Errorf("failed to copy kestrel: %w", err)
	}

	logging.Info("Kestrel agent installed")
	return nil
}

func ensureDestDir(rootfsPath, binDir string) error {
	info, err := os.Lstat(binDir)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(binDir)
			if readErr != nil {
				return fmt.Errorf("failed to read %s symlink: %w", binDir, readErr)
			}
			targetPath := resolveSymlinkTarget(rootfsPath, binDir, target)
			if rel, relErr := filepath.Rel(rootfsPath, targetPath); relErr != nil {
				return fmt.Errorf("failed to resolve symlink target for %s: %w", binDir, relErr)
			} else if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("symlink %s points outside rootfs: %s", binDir, target)
			}
			if mkErr := os.MkdirAll(targetPath, 0755); mkErr != nil {
				return fmt.Errorf("failed to prepare symlink target %s: %w", targetPath, mkErr)
			}
			return nil
		}
		if !info.IsDir() {
			return fmt.Errorf("/bin path exists but is not a directory: %s", binDir)
		}
		return nil
	case os.IsNotExist(err):
		if mkErr := os.MkdirAll(binDir, 0755); mkErr != nil {
			return fmt.Errorf("failed to create /bin directory: %w", mkErr)
		}
		return nil
	default:
		return fmt.Errorf("failed to inspect /bin directory: %w", err)
	}
}

func resolveSymlinkTarget(rootfsPath, linkPath, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Join(rootfsPath, strings.TrimPrefix(target, "/"))
	}
	base := filepath.Dir(linkPath)
	return filepath.Clean(filepath.Join(base, target))
}

// applyMappings applies user-defined file mappings.
func (b *OCIRootfsBuilder) applyMappings() error {
	if len(b.Config.Mappings) == 0 {
		logging.Info("No custom file mappings to apply")
		return nil
	}

	logging.Info("Applying custom file mappings")

	rootfsPath := filepath.Join(b.UnpackedPath, "rootfs")

	// Prepare mappings
	mappings, err := PrepareFileMappings(b.Config.Mappings, b.WorkDir)
	if err != nil {
		return fmt.Errorf("failed to prepare mappings: %w", err)
	}

	// Apply mappings to the unpacked rootfs
	if err := ApplyFileMappings(mappings, rootfsPath); err != nil {
		return fmt.Errorf("failed to apply mappings: %w", err)
	}

	logging.Info("Custom file mappings applied")
	return nil
}

// createSquashfs creates a squashfs compressed read-only filesystem.
func (b *OCIRootfsBuilder) createSquashfs() error {
	rootfsPath := filepath.Join(b.UnpackedPath, "rootfs")

	// Verify rootfs exists
	if _, err := os.Stat(rootfsPath); err != nil {
		return fmt.Errorf("rootfs directory does not exist: %w", err)
	}

	compressionLevel := b.Config.Filesystem.CompressionLevel
	if compressionLevel == 0 {
		compressionLevel = 15 // default
	}

	logging.Info("Creating squashfs image", "compression_level", compressionLevel)

	// Build mksquashfs command
	// Note: xz compression uses -Xdict-size instead of -Xcompression-level
	// Dictionary size affects compression ratio (higher = better compression but more RAM)
	// Map compression level to dictionary size:
	// Low (1-7): 25% (fast, lower compression)
	// Medium (8-15): 50% (balanced, default)
	// High (16-22): 100% (best compression, more RAM)
	var dictSize string
	switch {
	case compressionLevel <= 7:
		dictSize = "25%"
	case compressionLevel <= 15:
		dictSize = "50%"
	default:
		dictSize = "100%"
	}

	args := []string{
		rootfsPath,
		b.ImagePath,
		"-comp", "xz", // xz compression (best for size)
		"-Xdict-size", dictSize, // dictionary size for xz
		"-noappend",    // don't append to existing image
		"-no-progress", // disable progress bar
	}

	cmd := exec.Command("mksquashfs", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mksquashfs failed: %w\nOutput: %s", err, string(output))
	}

	// Get final size
	info, err := os.Stat(b.ImagePath)
	if err != nil {
		return fmt.Errorf("failed to stat squashfs image: %w", err)
	}

	sizeMB := float64(info.Size()) / (1024 * 1024)
	logging.Info("Squashfs image created", "size_mb", fmt.Sprintf("%.2f", sizeMB))

	return nil
}

// createImageFile calculates disk size and creates the image file.
func (b *OCIRootfsBuilder) createImageFile() error {
	rootfsPath := filepath.Join(b.UnpackedPath, "rootfs")

	// Calculate rootfs size
	cmd := exec.Command("du", "-sk", rootfsPath)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to calculate rootfs size: %w", err)
	}

	parts := strings.Fields(string(output))
	if len(parts) < 1 {
		return fmt.Errorf("failed to parse du output: %q", string(output))
	}

	sizeKB, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("failed to parse size %q: %w", parts[0], err)
	}

	// Determine buffer (tiered if SizeBufferMB == 0)
	bufferMB := b.computeBufferMB(sizeKB)
	bufferKB := bufferMB * 1024
	totalSizeKB := sizeKB + bufferKB
	totalSizeBytes := totalSizeKB * 1024

	logging.Info("Calculated image size",
		"rootfs_kb", sizeKB,
		"buffer_kb", bufferKB,
		"total_kb", totalSizeKB)

	// Create image file
	if b.Config.Filesystem.Preallocate {
		// Use fallocate for preallocated space
		cmd := exec.Command("fallocate", "-l", strconv.Itoa(totalSizeBytes), b.ImagePath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("fallocate failed: %w\nOutput: %s", err, string(output))
		}
	} else {
		// Use sparse allocation with dd
		cmd := exec.Command("dd", "if=/dev/zero", "of="+b.ImagePath,
			"bs=1K", "count=0", "seek="+strconv.Itoa(totalSizeKB))
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dd failed: %w\nOutput: %s", err, string(output))
		}
	}

	logging.Debug("Image file created", "path", b.ImagePath)
	return nil
}

// computeBufferMB returns the buffer size in MB based on config and rootfs size.
// If SizeBufferMB > 0, that explicit value is used. Otherwise uses a percentage-based
// approach: 25% of rootfs size, with minimum 64MB (for kestrel bootstrap) and maximum 1GB.
func (b *OCIRootfsBuilder) computeBufferMB(rootfsKB int) int {
	if b.Config != nil && b.Config.Filesystem != nil && b.Config.Filesystem.SizeBufferMB > 0 {
		return b.Config.Filesystem.SizeBufferMB
	}

	sizeMB := rootfsKB / 1024

	// Use 25% of rootfs size as buffer
	bufferMB := sizeMB / 4

	// Enforce minimum 64MB (needed for kestrel bootstrap and system operations)
	const minBufferMB = 64
	if bufferMB < minBufferMB {
		bufferMB = minBufferMB
	}

	// Enforce maximum 1GB (reasonable upper bound)
	const maxBufferMB = 1024
	if bufferMB > maxBufferMB {
		bufferMB = maxBufferMB
	}

	return bufferMB
}

// createFilesystem creates the filesystem on the image file.
func (b *OCIRootfsBuilder) createFilesystem() error {
	fsType := b.Config.Filesystem.Type
	mkfsCmd := "mkfs." + fsType

	// Type-specific flags
	args := []string{}
	switch fsType {
	case "ext4":
		args = append(args, "-F")
	case "xfs":
		args = append(args, "-f")
	case "btrfs":
		args = append(args, "-f")
	}
	args = append(args, b.ImagePath)

	cmd := exec.Command(mkfsCmd, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w\nOutput: %s", mkfsCmd, err, string(output))
	}

	logging.Debug("Filesystem created", "type", fsType)
	return nil
}

// mountImage attaches the image to a loop device and mounts it.
func (b *OCIRootfsBuilder) mountImage() error {
	// Find and attach loop device
	cmd := exec.Command("losetup", "--find", "--show", b.ImagePath)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("losetup failed: %w\nOutput: %s", err, string(output))
	}

	b.LoopDevicePath = strings.TrimSpace(string(output))
	if b.LoopDevicePath == "" {
		return fmt.Errorf("losetup did not return a device path")
	}

	logging.Debug("Attached to loop device", "device", b.LoopDevicePath)

	// Mount the loop device
	cmd = exec.Command("mount", b.LoopDevicePath, b.MountPoint)
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount failed: %w\nOutput: %s", err, string(output))
	}

	logging.Debug("Image mounted", "mount_point", b.MountPoint)
	return nil
}

// copyRootfsToImage copies the unpacked rootfs to the mounted image with progress.
func (b *OCIRootfsBuilder) copyRootfsToImage() error {
	rootfsPath := filepath.Join(b.UnpackedPath, "rootfs")

	// Calculate total size for progress bar
	var totalSize int64
	err := filepath.WalkDir(rootfsPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to calculate total size: %w", err)
	}

	// Create progress bar
	bar := progressbar.NewOptions64(totalSize,
		progressbar.OptionSetDescription("Copying files"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(15),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
	)

	// Walk and copy files
	return filepath.WalkDir(rootfsPath, func(srcPath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(rootfsPath, srcPath)
		if err != nil {
			return err
		}

		destPath := filepath.Join(b.MountPoint, relPath)

		// Get file info
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to get info for %s: %w", srcPath, err)
		}

		if info.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("failed to read symlink %s: %w", srcPath, err)
			}
			return os.Symlink(target, destPath)
		}

		// Copy regular file
		srcFile, err := os.Open(srcPath)
		if err != nil {
			return fmt.Errorf("failed to open source %s: %w", srcPath, err)
		}
		defer srcFile.Close()

		destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return fmt.Errorf("failed to create destination %s: %w", destPath, err)
		}
		defer destFile.Close()

		// Copy with progress
		writer := io.MultiWriter(destFile, bar)
		_, err = io.Copy(writer, srcFile)
		return err
	})
}

// unmountImage unmounts the image and detaches the loop device.
func (b *OCIRootfsBuilder) unmountImage() error {
	// Unmount
	if b.MountPoint != "" {
		if _, err := os.Stat(b.MountPoint); err == nil {
			cmd := exec.Command("umount", b.MountPoint)
			output, err := cmd.CombinedOutput()
			if err != nil && !strings.Contains(string(output), "not mounted") {
				logging.Warn("Failed to unmount", "mount_point", b.MountPoint, "error", err)
			}
		}
	}

	// Detach loop device
	if b.LoopDevicePath != "" {
		cmd := exec.Command("losetup", "-d", b.LoopDevicePath)
		output, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(string(output), "No such device") {
			logging.Warn("Failed to detach loop device", "device", b.LoopDevicePath, "error", err)
		}
	}

	return nil
}

// shrinkFilesystem shrinks the filesystem to optimal size (ext4 only).
func (b *OCIRootfsBuilder) shrinkFilesystem() error {
	// Only ext4 supports shrinking
	if b.Config.Filesystem.Type != "ext4" {
		logging.Debug("Skipping shrink for non-ext4 filesystem")
		return nil
	}

	logging.Info("Shrinking filesystem while preserving free space buffer")

	// Run e2fsck before any resize operations
	cmd := exec.Command("e2fsck", "-f", "-y", b.ImagePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		// e2fsck may return non-zero even if it fixed issues; log and continue
		logging.Debug("e2fsck completed with non-zero exit", "output", string(output))
	}

	// Get current block count and block size
	cmd = exec.Command("dumpe2fs", "-h", b.ImagePath)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("dumpe2fs failed: %w", err)
	}

	var curBlocks, blockSize int64
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Block count:") {
			fmt.Sscanf(line, "Block count: %d", &curBlocks)
		} else if strings.HasPrefix(line, "Block size:") {
			fmt.Sscanf(line, "Block size: %d", &blockSize)
		}
	}
	if curBlocks == 0 || blockSize == 0 {
		return fmt.Errorf("failed to parse current filesystem size from dumpe2fs")
	}

	// Query minimal required size in blocks
	cmd = exec.Command("resize2fs", "-P", b.ImagePath)
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("resize2fs -P failed: %w\nOutput: %s", err, string(output))
	}

	var minBlocks int64
	// Expected line: "Estimated minimum size of the filesystem: N"
	sc := bufio.NewScanner(strings.NewReader(string(output)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "Estimated minimum size of the filesystem:") {
			// Parse last field as the block count
			fields := strings.Fields(line)
			if len(fields) > 0 {
				// last token should be N
				fmt.Sscanf(fields[len(fields)-1], "%d", &minBlocks)
			}
		}
	}
	if minBlocks == 0 {
		return fmt.Errorf("failed to parse minimum block count from resize2fs -P output: %q", string(output))
	}

	// Recalculate rootfs size to apply the same tiered buffer policy used at allocation time
	rootfsPath := filepath.Join(b.UnpackedPath, "rootfs")
	cmd = exec.Command("du", "-sk", rootfsPath)
	duOut, duErr := cmd.Output()
	var rootfsKB int
	if duErr == nil {
		parts := strings.Fields(string(duOut))
		if len(parts) >= 1 {
			if v, err := strconv.Atoi(parts[0]); err == nil {
				rootfsKB = v
			}
		}
	}
	// Fallback if du failed
	if rootfsKB == 0 {
		// Use minimal blocks as approximation
		rootfsKB = int(minBlocks * (blockSize / 1024))
	}

	// Compute buffer in blocks from tiered or explicit buffer MB
	bufferMB := b.computeBufferMB(rootfsKB)
	bufBlocks := int64(bufferMB) * 1024 * 1024 / blockSize
	// Ensure at least 1 block buffer to avoid zero-free-space images
	if bufBlocks < 1 {
		bufBlocks = 1
	}

	// Desired final size: minimal + buffer, but never larger than current size
	desiredBlocks := minBlocks + bufBlocks
	if desiredBlocks > curBlocks {
		desiredBlocks = curBlocks
	}

	// Only resize if it actually changes the size
	if desiredBlocks < curBlocks {
		// Shrink to desired size in filesystem blocks
		cmd = exec.Command("resize2fs", b.ImagePath, strconv.FormatInt(desiredBlocks, 10))
		if output, err = cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("resize2fs to target size failed: %w\nOutput: %s", err, string(output))
		}
	}

	// Truncate backing file to match filesystem size
	fsSize := desiredBlocks * blockSize
	if err := os.Truncate(b.ImagePath, fsSize); err != nil {
		return fmt.Errorf("failed to truncate image: %w", err)
	}

	sizeMB := float64(fsSize) / (1024 * 1024)
	logging.Info("Filesystem resized", "final_size_mb", fmt.Sprintf("%.2f", sizeMB), "free_buffer_mb", b.Config.Filesystem.SizeBufferMB)

	return nil
}

// moveToFinal moves the image to the final output location.
func (b *OCIRootfsBuilder) moveToFinal() error {
	// Ensure output directory exists
	outputDir := filepath.Dir(b.OutputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Move the file
	if err := os.Rename(b.ImagePath, b.OutputPath); err != nil {
		return fmt.Errorf("failed to move image to %s: %w", b.OutputPath, err)
	}

	logging.Debug("Moved image to final location", "path", b.OutputPath)
	return nil
}

// cleanup performs cleanup operations.
func (b *OCIRootfsBuilder) cleanup() {
	// Try to unmount and detach if needed
	if b.MountPoint != "" || b.LoopDevicePath != "" {
		b.unmountImage()
	}

	// Remove ephemeral docker image tag if created
	if b.EphemeralTag != "" {
		cmd := exec.Command("docker", "rmi", "-f", b.EphemeralTag)
		if output, err := cmd.CombinedOutput(); err != nil {
			logging.Warn("Failed to remove ephemeral docker image", "tag", b.EphemeralTag, "error", err, "output", string(output))
		} else {
			logging.Debug("Removed ephemeral docker image", "tag", b.EphemeralTag)
		}
	}
}

// copyFile is a helper to copy a single file.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// buildDockerfileIfNeeded builds a Dockerfile into a local image if configured.

// buildDockerfileIfNeeded uses BuildKit to build the configured Dockerfile directly into the unpacked rootfs.
func (b *OCIRootfsBuilder) buildDockerfileIfNeeded() error {
	df := b.Config.Source.Dockerfile
	if df == "" {
		return nil
	}

	// Resolve Dockerfile and context paths
	dfPath := df
	if !filepath.IsAbs(dfPath) {
		dfPath = filepath.Join(b.WorkDir, dfPath)
	}
	ctxDir := b.Config.Source.Context
	if ctxDir == "" {
		ctxDir = filepath.Dir(dfPath)
	}
	if !filepath.IsAbs(ctxDir) {
		ctxDir = filepath.Join(b.WorkDir, ctxDir)
	}

	// Destination rootfs directory
	destRootfs := filepath.Join(b.UnpackedPath, "rootfs")
	if err := os.MkdirAll(destRootfs, 0755); err != nil {
		return fmt.Errorf("failed to create dest rootfs dir: %w", err)
	}

	logging.Info("Building Dockerfile via BuildKit", "dockerfile", dfPath, "context", ctxDir, "dest", destRootfs)
	if err := invokeDockerfileBuilder(context.Background(), DockerfileBuildInput{
		Dockerfile: dfPath,
		ContextDir: ctxDir,
		Target:     b.Config.Source.Target,
		BuildArgs:  b.Config.Source.BuildArgs,
		DestDir:    destRootfs,
	}); err != nil {
		return fmt.Errorf("buildkit build failed: %w", err)
	}

	// Verify the rootfs was actually created
	if info, err := os.Stat(destRootfs); err != nil {
		return fmt.Errorf("buildkit export verification failed - rootfs does not exist: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("buildkit export verification failed - rootfs is not a directory")
	}

	// Ensure essential FHS directories exist for agent installation
	// BuildKit may not export empty directories, so we create them explicitly
	essentialDirs := []string{
		filepath.Join(destRootfs, "bin"),
		filepath.Join(destRootfs, "usr"),
		filepath.Join(destRootfs, "usr", "bin"),
		filepath.Join(destRootfs, "usr", "local"),
		filepath.Join(destRootfs, "usr", "local", "bin"),
		filepath.Join(destRootfs, "etc"),
		filepath.Join(destRootfs, "tmp"),
		filepath.Join(destRootfs, "var"),
	}

	for _, dir := range essentialDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create essential directory %s: %w", dir, err)
		}
	}

	logging.Debug("Essential FHS directories ensured in rootfs")

	b.RootfsReady = true
	logging.Info("Dockerfile build complete via BuildKit; rootfs prepared")
	return nil
}
