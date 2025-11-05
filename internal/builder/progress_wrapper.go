package builder

import (
	"context"
	"fmt"
	"os"

	"github.com/volantvm/fledge/internal/config"
	"github.com/volantvm/fledge/internal/progress"
)

// BuildWithProgress wraps the OCI rootfs build with progress tracking
func BuildWithProgress(ctx context.Context, cfg *config.Config, workDir, output string, tracker *progress.Tracker) error {
	// Start validation stage
	tracker.StartStage(progress.StageValidation, "Validating configuration")
	tracker.UpdateStage("Checking build prerequisites", 10, 0, 0)

	// Validate config
	if cfg.Source.Image == "" && cfg.Source.Dockerfile == "" {
		return fmt.Errorf("either source.image or source.dockerfile must be specified")
	}

	// Load manifest template
	manifestTplPath := findManifestTemplate(workDir)
	var manifestTpl *config.ManifestTemplate
	if manifestTplPath != "" {
		var err error
		manifestTpl, err = config.LoadManifestTemplate(manifestTplPath)
		if err != nil {
			return fmt.Errorf("failed to load manifest template: %w", err)
		}
		tracker.UpdateStage("Loaded manifest template", 50, 0, 0)
	}

	tracker.CompleteStage("Configuration validated")

	// Create builder
	builder := NewOCIRootfsBuilder(cfg, manifestTpl, workDir, output)

	// Download stage
	tracker.StartStage(progress.StageDownload, "Pulling container image")

	imageRef := cfg.Source.Image
	if imageRef == "" && cfg.Source.Dockerfile != "" {
		imageRef = "from Dockerfile"
	}
	tracker.UpdateStage(fmt.Sprintf("Fetching %s", imageRef), 30, 0, 0)
	tracker.UpdateStage("Copying layers from registry", 60, 0, 0)
	tracker.CompleteStage(fmt.Sprintf("Downloaded %s", imageRef))

	// Build stage
	tracker.StartStage(progress.StageBuild, "Building filesystem")
	tracker.UpdateStage("Creating temporary workspace", 10, 0, 0)

	// Execute the actual build
	if err := builder.Build(); err != nil {
		tracker.Error(err, progress.StageBuild)
		return err
	}

	// Finalize stage
	tracker.StartStage(progress.StageFinalize, "Finalizing build")
	tracker.UpdateStage("Writing manifest", 50, 0, 0)

	// Get output file size
	fi, err := os.Stat(output)
	size := int64(0)
	if err == nil {
		size = fi.Size()
	}

	tracker.UpdateStage(fmt.Sprintf("Output: %s (%s)", output, progress.FormatBytes(size)), 100, size, size)
	tracker.CompleteStage("Build complete")

	return nil
}

// BuildInitramfsWithProgress wraps the initramfs build with progress tracking
func BuildInitramfsWithProgress(ctx context.Context, cfg *config.Config, workDir, output string, tracker *progress.Tracker) error {
	// Start validation stage
	tracker.StartStage(progress.StageValidation, "Validating initramfs configuration")
	tracker.UpdateStage("Checking agent configuration", 30, 0, 0)

	// Load manifest template
	manifestTplPath := findManifestTemplate(workDir)
	var manifestTpl *config.ManifestTemplate
	if manifestTplPath != "" {
		var err error
		manifestTpl, err = config.LoadManifestTemplate(manifestTplPath)
		if err != nil {
			return fmt.Errorf("failed to load manifest template: %w", err)
		}
		tracker.UpdateStage("Loaded manifest template", 70, 0, 0)
	}

	tracker.CompleteStage("Configuration validated")

	// Download stage
	tracker.StartStage(progress.StageDownload, "Fetching build dependencies")
	tracker.UpdateStage("Downloading agent binary", 40, 0, 0)
	tracker.UpdateStage("Downloading BusyBox", 70, 0, 0)
	tracker.CompleteStage("Dependencies downloaded")

	// Package stage
	tracker.StartStage(progress.StagePackage, "Packaging initramfs")
	tracker.UpdateStage("Assembling filesystem", 20, 0, 0)
	tracker.UpdateStage("Applying file mappings", 50, 0, 0)

	// Create builder and execute
	builder := NewInitramfsBuilder(cfg, manifestTpl, workDir, output)
	if err := builder.Build(); err != nil {
		tracker.Error(err, progress.StagePackage)
		return err
	}

	// Compress stage
	tracker.StartStage(progress.StageCompress, "Compressing archive")
	tracker.UpdateStage("Creating cpio archive", 40, 0, 0)
	tracker.UpdateStage("Compressing with gzip", 80, 0, 0)
	tracker.CompleteStage("Archive compressed")

	// Finalize
	tracker.StartStage(progress.StageFinalize, "Finalizing build")

	// Get output file size
	fi, err := os.Stat(output)
	size := int64(0)
	if err == nil {
		size = fi.Size()
	}

	tracker.UpdateStage(fmt.Sprintf("Output: %s (%s)", output, progress.FormatBytes(size)), 100, size, size)
	tracker.CompleteStage("Build complete")

	return nil
}

// findManifestTemplate looks for manifest.toml or manifest.json in workDir
func findManifestTemplate(workDir string) string {
	candidates := []string{
		workDir + "/manifest.toml",
		workDir + "/manifest.json",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
