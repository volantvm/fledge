package builder

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/volantvm/fledge/internal/config"
	"github.com/volantvm/fledge/internal/logging"
	"github.com/volantvm/fledge/internal/utils"
)

//go:embed embed/init.c
var initCSource string

const (
	// ReproducibleEpoch is the timestamp used for reproducible builds (2024-01-01)
	ReproducibleEpoch = 1704067200
)

// InitramfsBuilder builds initramfs archives following the Volant specification.
type InitramfsBuilder struct {
	Config           *config.Config
	ManifestTpl      *config.ManifestTemplate
	WorkDir          string
	RootfsDir        string
	OutputPath       string
	EphemeralTag     string
	BusyboxLocalPath string
}

// NewInitramfsBuilder creates a new initramfs builder.
func NewInitramfsBuilder(cfg *config.Config, manifestTpl *config.ManifestTemplate, workDir, outputPath string) *InitramfsBuilder {
	return &InitramfsBuilder{
		Config:      cfg,
		ManifestTpl: manifestTpl,
		WorkDir:     workDir,
		OutputPath:  outputPath,
	}
}

// Build creates the initramfs archive.
func (b *InitramfsBuilder) Build() error {
	logging.Info("Building initramfs", "output", b.OutputPath)

	// Create temporary directory for rootfs
	tmpDir, err := os.MkdirTemp("", "fledge-initramfs-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	b.RootfsDir = tmpDir
	logging.Debug("Created rootfs directory", "path", b.RootfsDir)

	// Build steps
	if err := b.setupDirectoryStructure(); err != nil {
		return fmt.Errorf("failed to setup directory structure: %w", err)
	}

	// Install kernel modules for squashfs and overlay
	if err := b.installKernelModules(); err != nil {
		logging.Warn("Failed to install kernel modules (they may be built-in to kernel)", "error", err)
	}

	// 1) Overlay Docker rootfs if provided (Dockerfile/image)
	if err := b.overlayDockerRootfsIfProvided(); err != nil {
		return fmt.Errorf("failed to overlay docker rootfs: %w", err)
	}

	if err := b.installBusybox(); err != nil {
		return fmt.Errorf("failed to install busybox: %w", err)
	}

	// Determine init mode and handle accordingly (after busybox is present)
	initMode := b.getInitMode()
	logging.Info("Init mode detected", "mode", initMode)

	switch initMode {
	case "default":
		// Mode 1: C init + Kestrel (batteries-included)
		if err := b.compileInit(); err != nil {
			return fmt.Errorf("failed to compile init: %w", err)
		}
		if err := b.installAgent(); err != nil {
			return fmt.Errorf("failed to install agent: %w", err)
		}

	case "custom":
		// Mode 2: User's custom init binary as PID 1
		if err := b.installCustomInit(); err != nil {
			return fmt.Errorf("failed to install custom init: %w", err)
		}
		logging.Info("Custom init configured", "path", b.Config.Init.Path)

	case "none":
		// Mode 3: No init wrapper - user must provide init via mappings
		logging.Info("No init wrapper - user must provide init via mappings")
		// Skip compileInit() and installAgent()
	}

	if err := b.applyMappings(); err != nil {
		return fmt.Errorf("failed to apply file mappings: %w", err)
	}

	if err := b.normalizeTimestamps(); err != nil {
		return fmt.Errorf("failed to normalize timestamps: %w", err)
	}

	if err := b.createArchive(); err != nil {
		return fmt.Errorf("failed to create archive: %w", err)
	}

	// Generate manifest.json
	if err := b.generateManifest(); err != nil {
		return fmt.Errorf("failed to generate manifest: %w", err)
	}

	logging.Info("Initramfs build complete", "output", b.OutputPath)
	return nil
}

// setupDirectoryStructure creates the FHS directory structure.
func (b *InitramfsBuilder) setupDirectoryStructure() error {
	logging.Info("Setting up directory structure")

	dirs := []string{
		"/bin",
		"/sbin",
		"/etc",
		"/proc",
		"/sys",
		"/dev",
		"/tmp",
		"/run",
		"/usr/bin",
		"/usr/sbin",
		"/usr/lib",
		"/var/log",
	}

	for _, dir := range dirs {
		fullPath := filepath.Join(b.RootfsDir, dir)
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	logging.Debug("Directory structure created")
	return nil
}

// installKernelModules copies essential kernel modules (squashfs, overlay) into the initramfs.
// This allows the init to load these modules if they're not built-in to the kernel.
func (b *InitramfsBuilder) installKernelModules() error {
	logging.Info("Installing kernel modules")

	// Determine kernel version from running system
	cmd := exec.Command("uname", "-r")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to detect kernel version: %w", err)
	}
	kernelVersion := strings.TrimSpace(string(output))

	// Common module locations
	moduleBasePaths := []string{
		fmt.Sprintf("/lib/modules/%s/kernel/fs", kernelVersion),
		"/lib/modules/kernel/fs", // Generic fallback
	}

	// Modules we need
	requiredModules := []string{
		"squashfs/squashfs.ko",
		"squashfs/squashfs.ko.xz",
		"squashfs/squashfs.ko.gz",
		"overlayfs/overlay.ko",
		"overlayfs/overlay.ko.xz",
		"overlayfs/overlay.ko.gz",
		"overlay.ko",
		"overlay.ko.xz",
		"overlay.ko.gz",
	}

	// Create /lib/modules directory in initramfs
	modulesDir := filepath.Join(b.RootfsDir, "lib", "modules")
	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		return fmt.Errorf("failed to create modules directory: %w", err)
	}

	foundAny := false

	// Try to find and copy modules
	for _, basePath := range moduleBasePaths {
		for _, modPath := range requiredModules {
			fullPath := filepath.Join(basePath, modPath)
			if _, err := os.Stat(fullPath); err == nil {
				// Found a module, copy it
				destName := filepath.Base(modPath)
				destPath := filepath.Join(modulesDir, destName)

				if err := CopyFile(fullPath, destPath, 0644); err != nil {
					logging.Warn("Failed to copy kernel module", "module", fullPath, "error", err)
					continue
				}

				logging.Info("Installed kernel module", "module", destName)
				foundAny = true
			}
		}
	}

	if !foundAny {
		return fmt.Errorf("no kernel modules found - ensure squashfs and overlay modules are available, or use a kernel with them built-in")
	}

	return nil
}

// compileInit compiles the init.c source to /init.
func (b *InitramfsBuilder) compileInit() error {
	logging.Info("Compiling init binary")

	// Write init.c to temp file
	initCPath := filepath.Join(b.RootfsDir, "init.c")
	if err := os.WriteFile(initCPath, []byte(initCSource), 0644); err != nil {
		return fmt.Errorf("failed to write init.c: %w", err)
	}

	// Compile with gcc
	initBinaryPath := filepath.Join(b.RootfsDir, "init")
	cmd := exec.Command("gcc",
		"-static",
		"-Os",
		"-Wall",
		"-o", initBinaryPath,
		initCPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gcc compilation failed: %w\nOutput: %s", err, string(output))
	}

	// Remove the source file
	os.Remove(initCPath)

	// Ensure init is executable
	if err := os.Chmod(initBinaryPath, 0755); err != nil {
		return fmt.Errorf("failed to chmod init: %w", err)
	}

	logging.Info("Init binary compiled successfully")
	return nil
}

// installBusybox installs busybox with symlinks, sourcing from host when available.
func (b *InitramfsBuilder) installBusybox() error {
	busyboxPath := filepath.Join(b.RootfsDir, "bin", "busybox")

	if b.BusyboxLocalPath != "" {
		logging.Info("Installing busybox from host", "path", b.BusyboxLocalPath)
		if err := CopyFile(b.BusyboxLocalPath, busyboxPath, 0755); err != nil {
			return fmt.Errorf("failed to copy busybox from host: %w", err)
		}
	} else {
		logging.Info("Installing busybox", "url", b.Config.Source.BusyboxURL)

		// Download busybox
		tmpPath, err := utils.DownloadToTempFile(b.Config.Source.BusyboxURL, true)
		if err != nil {
			return fmt.Errorf("failed to download busybox: %w", err)
		}
		defer os.Remove(tmpPath)

		// Verify checksum if provided
		if b.Config.Source.BusyboxSHA256 != "" {
			logging.Info("Verifying busybox checksum")
			if err := utils.VerifyChecksum(tmpPath, b.Config.Source.BusyboxSHA256); err != nil {
				return fmt.Errorf("busybox checksum verification failed: %w", err)
			}
		}

		if err := CopyFile(tmpPath, busyboxPath, 0755); err != nil {
			return fmt.Errorf("failed to copy busybox: %w", err)
		}
	}

	// Create busybox symlinks
	if err := b.createBusyboxSymlinks(); err != nil {
		return fmt.Errorf("failed to create busybox symlinks: %w", err)
	}

	logging.Info("Busybox installed successfully")
	return nil
}

// createBusyboxSymlinks creates symlinks for common busybox applets.
func (b *InitramfsBuilder) createBusyboxSymlinks() error {
	logging.Debug("Creating busybox symlinks")

	// Common busybox applets
	applets := []string{
		"sh", "ash", "ls", "cat", "cp", "mv", "rm", "mkdir", "rmdir",
		"ln", "chmod", "chown", "ps", "kill", "mount", "umount",
		"grep", "sed", "awk", "find", "test", "echo", "printf",
		"true", "false", "sleep", "pwd", "cd", "env", "which",
		"tar", "gzip", "gunzip", "wget", "vi",
	}

	binDir := filepath.Join(b.RootfsDir, "bin")
	for _, applet := range applets {
		linkPath := filepath.Join(binDir, applet)
		if err := os.Symlink("busybox", linkPath); err != nil {
			logging.Warn("Failed to create symlink", "applet", applet, "error", err)
		}
	}

	logging.Debug("Busybox symlinks created")
	return nil
}

// installAgent installs the kestrel agent binary.
func (b *InitramfsBuilder) installAgent() error {
	logging.Info("Installing kestrel agent")

	// Source the agent
	agentPath, err := SourceAgent(b.Config.Agent, true)
	if err != nil {
		return fmt.Errorf("failed to source agent: %w", err)
	}
	defer CleanupAgent(agentPath)

	// Copy agent to /bin/kestrel
	kestrelPath := filepath.Join(b.RootfsDir, "bin", "kestrel")
	if err := ensureDestDir(b.RootfsDir, filepath.Dir(kestrelPath)); err != nil {
		return err
	}
	if err := CopyFile(agentPath, kestrelPath, 0755); err != nil {
		return fmt.Errorf("failed to copy kestrel: %w", err)
	}

	logging.Info("Kestrel agent installed")
	return nil
}

// overlayDockerRootfsIfProvided builds (if needed) and overlays a Docker image rootfs onto the initramfs root.
func (b *InitramfsBuilder) overlayDockerRootfsIfProvided() error {
	// If Dockerfile provided, use BuildKit to export rootfs and overlay
	if b.Config.Source.Dockerfile != "" {
		dfPath := b.Config.Source.Dockerfile
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

		exportDir, err := os.MkdirTemp("", "fledge-init-df-rootfs-*")
		if err != nil {
			return fmt.Errorf("failed to create export dir: %w", err)
		}
		defer os.RemoveAll(exportDir)

		logging.Info("Building Dockerfile via BuildKit for initramfs overlay", "dockerfile", dfPath, "context", ctxDir)
		err = invokeDockerfileBuilder(context.Background(), DockerfileBuildInput{
			Dockerfile: dfPath,
			ContextDir: ctxDir,
			Target:     b.Config.Source.Target,
			BuildArgs:  b.Config.Source.BuildArgs,
			DestDir:    exportDir,
		})
		if err != nil {
			return fmt.Errorf("buildkit build failed: %w", err)
		}

		// Overlay exported rootfs (exportDir contains the full rootfs)
		if err := overlayCopyPreserve(exportDir, b.RootfsDir); err != nil {
			return fmt.Errorf("failed to overlay buildkit rootfs: %w", err)
		}
		return nil
	}

	// If an image reference is provided, fetch via skopeo/umoci and overlay
	imgRef := b.Config.Source.Image
	if imgRef == "" {
		// Nothing to overlay
		return nil
	}

	// Create temp oci layout and unpack
	tmpDir, err := os.MkdirTemp("", "fledge-init-overlay-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	ociLayout := filepath.Join(tmpDir, "oci-layout")
	unpackDir := filepath.Join(tmpDir, "unpacked")
	if err := os.MkdirAll(ociLayout, 0755); err != nil {
		return fmt.Errorf("failed to create oci layout dir: %w", err)
	}

	// Try local docker-daemon first
	cmd := exec.Command("skopeo", "copy",
		fmt.Sprintf("docker-daemon:%s", imgRef),
		fmt.Sprintf("oci:%s:latest", ociLayout))
	if output, err := cmd.CombinedOutput(); err != nil {
		// Try remote registry fallback
		cmd = exec.Command("skopeo", "copy",
			fmt.Sprintf("docker://%s", imgRef),
			fmt.Sprintf("oci:%s:latest", ociLayout))
		if output2, err2 := cmd.CombinedOutput(); err2 != nil {
			return fmt.Errorf("skopeo copy failed: %w\nLocal output: %s\nRemote output: %s", err2, string(output), string(output2))
		}
	}

	// Unpack
	if err := os.MkdirAll(unpackDir, 0755); err != nil {
		return fmt.Errorf("failed to create unpack dir: %w", err)
	}
	cmd = exec.Command("umoci", "unpack", "--image", fmt.Sprintf("%s:latest", ociLayout), unpackDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("umoci unpack failed: %w\nOutput: %s", err, string(output))
	}

	// Overlay the unpacked rootfs onto b.RootfsDir
	srcRoot := filepath.Join(unpackDir, "rootfs")
	if err := overlayCopyPreserve(srcRoot, b.RootfsDir); err != nil {
		return fmt.Errorf("failed to overlay rootfs: %w", err)
	}

	return nil
}

// overlayCopyPreserve copies srcRoot onto dstRoot preserving file modes and symlinks.
func overlayCopyPreserve(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(srcPath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(dstRoot, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			// Remove existing path if any to avoid dangling copies
			_ = os.RemoveAll(dstPath)
			return os.Symlink(target, dstPath)
		}

		// Regular file
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			return err
		}
		srcFile, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer srcFile.Close()
		dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()
		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return err
		}
		return nil
	})
}

// applyMappings applies user-defined file mappings.
func (b *InitramfsBuilder) applyMappings() error {
	if len(b.Config.Mappings) == 0 {
		logging.Info("No custom file mappings to apply")
		return nil
	}

	logging.Info("Applying custom file mappings")

	// Prepare mappings
	mappings, err := PrepareFileMappings(b.Config.Mappings, b.WorkDir)
	if err != nil {
		return fmt.Errorf("failed to prepare mappings: %w", err)
	}

	// Apply mappings
	if err := ApplyFileMappings(mappings, b.RootfsDir); err != nil {
		return fmt.Errorf("failed to apply mappings: %w", err)
	}

	logging.Info("Custom file mappings applied")
	return nil
}

// normalizeTimestamps sets all file timestamps to a reproducible epoch for deterministic builds.
func (b *InitramfsBuilder) normalizeTimestamps() error {
	logging.Info("Normalizing timestamps for reproducible builds")

	epoch := time.Unix(ReproducibleEpoch, 0)

	err := filepath.Walk(b.RootfsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Set mtime and atime to epoch
		if err := os.Chtimes(path, epoch, epoch); err != nil {
			return fmt.Errorf("failed to change time for %s: %w", path, err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to normalize timestamps: %w", err)
	}

	logging.Info("Timestamps normalized")
	return nil
}

// createArchive creates the compressed CPIO archive.
func (b *InitramfsBuilder) createArchive() error {
	logging.Info("Creating CPIO archive")

	// Ensure output directory exists
	outputDir := filepath.Dir(b.OutputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create a temporary file for the uncompressed CPIO
	tmpCpio, err := os.CreateTemp("", "fledge-cpio-*")
	if err != nil {
		return fmt.Errorf("failed to create temp cpio file: %w", err)
	}
	tmpCpioPath := tmpCpio.Name()
	tmpCpio.Close()
	defer os.Remove(tmpCpioPath)

	// Use find + cpio to create the archive
	// We change to the rootfs directory to get relative paths
	findCmd := exec.Command("find", ".", "-print0")
	findCmd.Dir = b.RootfsDir

	cpioCmd := exec.Command("cpio", "--null", "-ov", "--format=newc")
	cpioCmd.Dir = b.RootfsDir

	// Create the output file for cpio
	cpioOut, err := os.Create(tmpCpioPath)
	if err != nil {
		return fmt.Errorf("failed to create cpio output: %w", err)
	}

	// Pipe find output to cpio
	cpioCmd.Stdin, err = findCmd.StdoutPipe()
	if err != nil {
		cpioOut.Close()
		return fmt.Errorf("failed to create pipe: %w", err)
	}

	cpioCmd.Stdout = cpioOut
	var cpioStderr strings.Builder
	cpioCmd.Stderr = &cpioStderr

	// Start cpio
	if err := cpioCmd.Start(); err != nil {
		cpioOut.Close()
		return fmt.Errorf("failed to start cpio: %w", err)
	}

	// Start find
	if err := findCmd.Start(); err != nil {
		cpioOut.Close()
		cpioCmd.Wait()
		return fmt.Errorf("failed to start find: %w", err)
	}

	// Wait for both commands
	if err := findCmd.Wait(); err != nil {
		cpioOut.Close()
		cpioCmd.Wait()
		return fmt.Errorf("find command failed: %w", err)
	}

	if err := cpioCmd.Wait(); err != nil {
		cpioOut.Close()
		return fmt.Errorf("cpio command failed: %w\nStderr: %s", err, cpioStderr.String())
	}

	cpioOut.Close()

	// Compress the CPIO with gzip (use -n for reproducibility)
	logging.Info("Compressing archive with gzip")

	cpioFile, err := os.Open(tmpCpioPath)
	if err != nil {
		return fmt.Errorf("failed to open cpio file: %w", err)
	}
	defer cpioFile.Close()

	outputFile, err := os.Create(b.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	gzipCmd := exec.Command("gzip", "-n", "-9")
	gzipCmd.Stdin = cpioFile
	gzipCmd.Stdout = outputFile

	var gzipStderr strings.Builder
	gzipCmd.Stderr = &gzipStderr

	if err := gzipCmd.Run(); err != nil {
		return fmt.Errorf("gzip command failed: %w\nStderr: %s", err, gzipStderr.String())
	}

	logging.Info("Archive created successfully", "output", b.OutputPath)
	return nil
}

// getInitMode determines which init mode is configured.
// Returns "default", "custom", or "none".
func (b *InitramfsBuilder) getInitMode() string {
	if b.Config.Init == nil {
		return "default"
	}
	if b.Config.Init.None {
		return "none"
	}
	if b.Config.Init.Path != "" {
		return "custom"
	}
	return "default"
}

// writeCustomInitPath writes the custom init path to /.volant_init
// so the C init knows what to exec.
func (b *InitramfsBuilder) installCustomInit() error {
	logging.Info("Installing custom init binary", "source", b.Config.Init.Path)

	// Resolve the source path relative to WorkDir
	srcPath := b.Config.Init.Path
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(b.WorkDir, srcPath)
	}

	// Check if source file exists
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return fmt.Errorf("custom init file not found: %s", srcPath)
	}

	// Copy to /init in rootfs
	initDest := filepath.Join(b.RootfsDir, "init")

	// Read source file
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read custom init: %w", err)
	}

	// Write to destination
	if err := os.WriteFile(initDest, data, 0755); err != nil {
		return fmt.Errorf("failed to write custom init: %w", err)
	}

	logging.Info("Custom init binary installed successfully")
	return nil
}

// generateManifest creates the manifest.json file by merging the manifest template
// with build metadata (checksum, URL, format).
func (b *InitramfsBuilder) generateManifest() error {
	logging.Info("Generating manifest.json")

	// Compute SHA256 checksum of the built initramfs
	checksum, err := computeInitramfsSHA256(b.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	logging.Info("Computed initramfs checksum", "sha256", checksum)

	// Build the final manifest by merging template + build metadata
	manifest := make(map[string]interface{})

	// Copy fields from manifest template
	if b.ManifestTpl != nil {
		manifest["schema_version"] = b.ManifestTpl.SchemaVersion
		manifest["name"] = b.ManifestTpl.Name
		manifest["version"] = b.ManifestTpl.Version
		manifest["runtime"] = b.ManifestTpl.Runtime

		// Resources
		if b.ManifestTpl.Resources != nil {
			manifest["resources"] = map[string]interface{}{
				"cpu_cores": b.ManifestTpl.Resources.CPUCores,
				"memory_mb": b.ManifestTpl.Resources.MemoryMB,
			}
		}

		// Workload
		if b.ManifestTpl.Workload != nil {
			workload := map[string]interface{}{
				"entrypoint": b.ManifestTpl.Workload.Entrypoint,
			}
			if len(b.ManifestTpl.Workload.Args) > 0 {
				workload["args"] = b.ManifestTpl.Workload.Args
			}
			manifest["workload"] = workload
		}

		// Environment variables
		if len(b.ManifestTpl.Env) > 0 {
			manifest["env"] = b.ManifestTpl.Env
		}

		// Network
		if b.ManifestTpl.Network != nil {
			network := map[string]interface{}{
				"mode": b.ManifestTpl.Network.Mode,
			}
			if len(b.ManifestTpl.Network.Expose) > 0 {
				expose := make([]map[string]interface{}, len(b.ManifestTpl.Network.Expose))
				for i, port := range b.ManifestTpl.Network.Expose {
					portMap := map[string]interface{}{
						"port":     port.Port,
						"protocol": port.Protocol,
					}
					if port.HostPort > 0 {
						portMap["host_port"] = port.HostPort
					}
					expose[i] = portMap
				}
				network["expose"] = expose
			}
			manifest["network"] = network
		}

		// Actions
		if len(b.ManifestTpl.Actions) > 0 {
			actions := make(map[string]interface{})
			for name, action := range b.ManifestTpl.Actions {
				actions[name] = map[string]interface{}{
					"path":   action.Path,
					"method": action.Method,
				}
			}
			manifest["actions"] = actions
		}

		// Cloud-init
		if b.ManifestTpl.CloudInit != nil {
			cloudInit := make(map[string]interface{})
			if b.ManifestTpl.CloudInit.Datasource != "" {
				cloudInit["datasource"] = b.ManifestTpl.CloudInit.Datasource
			}
			if b.ManifestTpl.CloudInit.UserData != nil {
				userData := map[string]interface{}{
					"inline":  b.ManifestTpl.CloudInit.UserData.Inline,
					"content": b.ManifestTpl.CloudInit.UserData.Content,
				}
				cloudInit["user_data"] = userData
			}
			if len(b.ManifestTpl.CloudInit.MetaData) > 0 {
				cloudInit["meta_data"] = b.ManifestTpl.CloudInit.MetaData
			}
			if len(cloudInit) > 0 {
				manifest["cloud_init"] = cloudInit
			}
		}

		// Devices
		if b.ManifestTpl.Devices != nil && len(b.ManifestTpl.Devices.PCIPassthrough) > 0 {
			manifest["devices"] = map[string]interface{}{
				"pci_passthrough": b.ManifestTpl.Devices.PCIPassthrough,
			}
		}
	}

	// Add build metadata - initramfs section
	// The initramfs format is always cpio.gz for this builder
	manifest["initramfs"] = map[string]interface{}{
		"url":      "file://" + b.OutputPath,
		"format":   "cpio.gz",
		"checksum": "sha256:" + checksum,
	}

	// Write manifest.json
	manifestPath := b.OutputPath + ".manifest.json"
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest JSON: %w", err)
	}

	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		return fmt.Errorf("failed to write manifest file: %w", err)
	}

	logging.Info("Manifest generated successfully", "path", manifestPath)
	return nil
}

// computeInitramfsSHA256 computes the SHA256 checksum of the initramfs file.
func computeInitramfsSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", fmt.Errorf("failed to compute hash: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
