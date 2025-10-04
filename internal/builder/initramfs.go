package builder

import (
	_ "embed"
	"fmt"
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
	Config     *config.Config
	WorkDir    string
	RootfsDir  string
	OutputPath string
}

// NewInitramfsBuilder creates a new initramfs builder.
func NewInitramfsBuilder(cfg *config.Config, workDir, outputPath string) *InitramfsBuilder {
	return &InitramfsBuilder{
		Config:     cfg,
		WorkDir:    workDir,
		OutputPath: outputPath,
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

	// Determine init mode and handle accordingly
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

	if err := b.installBusybox(); err != nil {
		return fmt.Errorf("failed to install busybox: %w", err)
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

// installBusybox downloads and installs busybox with symlinks.
func (b *InitramfsBuilder) installBusybox() error {
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

	// Copy busybox to /bin/busybox
	busyboxPath := filepath.Join(b.RootfsDir, "bin", "busybox")
	if err := CopyFile(tmpPath, busyboxPath, 0755); err != nil {
		return fmt.Errorf("failed to copy busybox: %w", err)
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
	if err := CopyFile(agentPath, kestrelPath, 0755); err != nil {
		return fmt.Errorf("failed to copy kestrel: %w", err)
	}

	logging.Info("Kestrel agent installed")
	return nil
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
