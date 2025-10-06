// Fledge - Volant Plugin Builder
// Copyright (c) 2025 HYPR. PTE. LTD.
// Licensed under the Business Source License 1.1
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/volantvm/fledge/internal/builder"
	"github.com/volantvm/fledge/internal/config"
	"github.com/volantvm/fledge/internal/logging"
)

var (
	// Version information - set via ldflags during build
	version   = "dev"
	buildDate = "unknown"
	gitCommit = "unknown"

	// Global flags
	verbose bool
	quiet   bool
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "fledge",
		Short: "Fledge - Volant Plugin Builder",
		Long: `Fledge is a production-grade CLI tool for building Volant plugin artifacts.

It supports two build strategies:
  1. OCI Rootfs: Convert OCI images to bootable ext4/xfs/btrfs filesystem images
  2. Initramfs: Build minimal initramfs archives with busybox, kestrel agent, and applications

The tool reads declarative fledge.toml configuration files and produces
ready-to-deploy artifacts following the Filesystem Hierarchy Standard (FHS).`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			logging.InitLogger(verbose, quiet)
		},
	}

	// Global flags
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output with debug details")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "quiet mode (minimal output, errors only)")

	// Add subcommands
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newBuildCommand())

	return rootCmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("fledge version %s\n", version)
			fmt.Printf("  build date: %s\n", buildDate)
			fmt.Printf("  git commit: %s\n", gitCommit)
		},
	}
}

func newBuildCommand() *cobra.Command {
	var (
		configPath string
		outputPath string
	)

	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build a plugin artifact from fledge.toml",
		Long: `Build a plugin artifact based on the declarative fledge.toml configuration.

The tool will automatically detect the build strategy (oci_rootfs or initramfs)
from the configuration file and produce the appropriate artifact.

Examples:
  # Build using default fledge.toml in current directory
  fledge build

  # Build with custom config and output path
  fledge build -c path/to/fledge.toml -o my-plugin.img

  # Verbose build with detailed logging
  fledge build -v

  # Quiet build (only show errors and final output path)
  fledge build -q`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(configPath, outputPath)
		},
	}

	buildCmd.Flags().StringVarP(&configPath, "config", "c", "fledge.toml", "path to fledge.toml configuration file")
	buildCmd.Flags().StringVarP(&outputPath, "output", "o", "", "output file path (default: auto-generated from config)")

	return buildCmd
}

func runBuild(configPath, outputPath string) error {
	logging.Info("Starting Fledge build", "config", configPath)

	// Setup signal handling for graceful cleanup
	ctx, cancel := setupSignalHandling()
	defer cancel()

	// Check if running as root (required for loop devices, mounts, etc.)
	if os.Geteuid() != 0 {
		logging.Error("Fledge requires root privileges for building artifacts")
		return fmt.Errorf("must run as root (use sudo)")
	}

	// Load and validate configuration
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	// Determine output path
	output := determineOutputPath(cfg, outputPath)
	logging.Info("Output artifact", "path", output)

	// Get working directory (where config file is located)
	workDir, err := getWorkingDirectory(configPath)
	if err != nil {
		return err
	}

	// Build based on strategy
	switch cfg.Strategy {
	case "oci_rootfs":
		return buildOCIRootfs(ctx, cfg, workDir, output)
	case "initramfs":
		return buildInitramfs(ctx, cfg, workDir, output)
	default:
		return fmt.Errorf("unknown build strategy: %s", cfg.Strategy)
	}
}

// setupSignalHandling configures graceful shutdown on SIGINT/SIGTERM.
func setupSignalHandling() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logging.Warn("Received signal, initiating graceful shutdown", "signal", sig)
		cancel()
	}()

	return ctx, cancel
}

// loadConfig loads and validates the configuration file.
func loadConfig(configPath string) (*config.Config, error) {
	logging.Debug("Loading configuration", "path", configPath)

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found: %s", configPath)
	}

	// Parse configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		logging.Error("Failed to load configuration", "error", err)
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	logging.Info("Configuration loaded successfully",
		"strategy", cfg.Strategy)

	return cfg, nil
}

// getWorkingDirectory determines the working directory from the config path.
func getWorkingDirectory(configPath string) (string, error) {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve config path: %w", err)
	}

	workDir := filepath.Dir(absPath)
	logging.Debug("Working directory", "path", workDir)

	return workDir, nil
}

// determineOutputPath determines the final output path for the artifact.
func determineOutputPath(cfg *config.Config, outputPath string) string {
	// If user specified output path, use it
	if outputPath != "" {
		return outputPath
	}

	// Auto-generate based on strategy
	ext := getOutputExtension(cfg.Strategy)
	var baseName string

	// Try to derive a meaningful name from the config
	switch cfg.Strategy {
	case "oci_rootfs":
		// Use image name as base (e.g., "nginx:latest" -> "nginx")
		if cfg.Source.Image != "" {
			baseName = extractImageName(cfg.Source.Image)
		} else {
			baseName = "plugin"
		}
	case "initramfs":
		baseName = "plugin"
	default:
		baseName = "plugin"
	}

	sanitizedName := sanitizeFilename(baseName)
	return fmt.Sprintf("%s%s", sanitizedName, ext)
}

// getOutputExtension returns the appropriate file extension for the strategy.
func getOutputExtension(strategy string) string {
	switch strategy {
	case "oci_rootfs":
		return ".img"
	case "initramfs":
		return ".cpio.gz"
	default:
		return ".bin"
	}
}

// extractImageName extracts a base name from a Docker image reference.
// Examples: "nginx:latest" -> "nginx", "docker.io/library/nginx" -> "nginx"
func extractImageName(imageRef string) string {
	// Remove tag (after :)
	if idx := strings.LastIndex(imageRef, ":"); idx > 0 {
		imageRef = imageRef[:idx]
	}

	// Remove digest (after @)
	if idx := strings.LastIndex(imageRef, "@"); idx > 0 {
		imageRef = imageRef[:idx]
	}

	// Get last component after /
	if idx := strings.LastIndex(imageRef, "/"); idx >= 0 {
		imageRef = imageRef[idx+1:]
	}

	return imageRef
}

// sanitizeFilename removes/replaces invalid characters from filenames.
func sanitizeFilename(name string) string {
	// Replace spaces and slashes with hyphens
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")

	// Convert to lowercase
	return strings.ToLower(name)
}

// buildOCIRootfs builds an OCI rootfs filesystem image.
func buildOCIRootfs(ctx context.Context, cfg *config.Config, workDir, outputPath string) error {
	logging.Info("Building OCI rootfs artifact")

	// Validate OCI-specific requirements
	if cfg.Source.Image == "" && cfg.Source.Dockerfile == "" {
		return fmt.Errorf("either source.image or source.dockerfile is required for oci_rootfs strategy")
	}

	// Create builder
	builder := builder.NewOCIRootfsBuilder(cfg, workDir, outputPath)

	// Run build
	if err := builder.Build(); err != nil {
		logging.Error("OCI rootfs build failed", "error", err)
		return err
	}

	logging.Info("✓ OCI rootfs build complete", "output", outputPath)
	return nil
}

// buildInitramfs builds an initramfs CPIO archive.
func buildInitramfs(ctx context.Context, cfg *config.Config, workDir, outputPath string) error {
	logging.Info("Building initramfs artifact")

	// Create builder
	builder := builder.NewInitramfsBuilder(cfg, workDir, outputPath)

	// Run build
	if err := builder.Build(); err != nil {
		logging.Error("Initramfs build failed", "error", err)
		return err
	}

	logging.Info("✓ Initramfs build complete", "output", outputPath)
	return nil
}
