// Fledge - Volant Plugin Builder
// Copyright (c) 2025 HYPR. PTE. LTD.
// Licensed under the Business Source License 1.1
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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

	// TODO: Implement build logic in subsequent phases
	// This is a placeholder for Phase 1
	logging.Error("Build functionality not yet implemented")
	return fmt.Errorf("build functionality will be implemented in Phase 2-6")
}
