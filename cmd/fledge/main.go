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
	"github.com/volantvm/fledge/internal/server"
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
	rootCmd.AddCommand(newServeCommand())

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
		configPath      string
		outputPath      string
		dockerfilePath  string
		contextDir      string
		targetStage     string
		buildArgValues  []string
		outputInitramfs bool
	)

	buildCmd := &cobra.Command{
		Use:   "build [DOCKERFILE]",
		Short: "Build a plugin artifact from fledge.toml or a Dockerfile",
		Long: `Build Volant plugin artifacts from either a declarative fledge.toml configuration
or directly from a Dockerfile using the embedded BuildKit solver.

Examples:
  # Build using the default fledge.toml in the current directory
  sudo fledge build

  # Build from a specific config file and custom output path
  sudo fledge build -c path/to/fledge.toml -o dist/rootfs.img

  # Build directly from a Dockerfile (rootfs image output)
  sudo fledge build ./Dockerfile

  # Build an initramfs from a Dockerfile with custom context and build args
  sudo fledge build --dockerfile docker/app.Dockerfile --context ./app --build-arg VERSION=1.2.3 --output-initramfs`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if dockerfilePath != "" && dockerfilePath != args[0] {
					return fmt.Errorf("dockerfile specified multiple times with differing values")
				}
				dockerfilePath = args[0]
			}

			return runBuild(buildCLIOptions{
				ConfigPath:      configPath,
				OutputPath:      outputPath,
				DockerfilePath:  dockerfilePath,
				ContextDir:      contextDir,
				Target:          targetStage,
				BuildArgs:       buildArgValues,
				OutputInitramfs: outputInitramfs,
				ConfigExplicit:  cmd.Flags().Changed("config"),
			})
		},
	}

	buildCmd.Flags().StringVarP(&configPath, "config", "c", "fledge.toml", "path to fledge.toml configuration file")
	buildCmd.Flags().StringVarP(&outputPath, "output", "o", "", "output file path (default: auto-generated)")
	buildCmd.Flags().StringVar(&dockerfilePath, "dockerfile", "", "path to Dockerfile for direct-build mode (alternative to positional argument)")
	buildCmd.Flags().StringVar(&contextDir, "context", "", "build context directory (default: directory containing the Dockerfile)")
	buildCmd.Flags().StringVar(&targetStage, "target", "", "build target stage (for multi-stage Dockerfiles)")
	buildCmd.Flags().StringArrayVar(&buildArgValues, "build-arg", nil, "build argument in KEY=VALUE form (can be repeated)")
	buildCmd.Flags().BoolVar(&outputInitramfs, "output-initramfs", false, "produce an initramfs (.cpio.gz) instead of a rootfs image when building from a Dockerfile")

	return buildCmd
}

func newServeCommand() *cobra.Command {
	var (
		addr   string
		apiKey string
		cors   string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run fledge in HTTP daemon mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := setupSignalHandling()
			defer cancel()

			if addr == "" {
				if v := os.Getenv("FLEDGE_ADDR"); v != "" {
					addr = v
				} else {
					addr = "127.0.0.1:7070"
				}
			}
			if apiKey == "" {
				apiKey = os.Getenv("FLEDGE_API_KEY")
			}
			origins := []string{}
			if cors == "" {
				cors = os.Getenv("FLEDGE_CORS_ORIGINS")
			}
			if cors != "" {
				for _, p := range strings.Split(cors, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						origins = append(origins, p)
					}
				}
			}

			opts := server.Options{Addr: addr, APIKey: apiKey, CORSOrigins: origins}
			logging.Info("Starting fledge serve", "addr", opts.Addr)

			// wrap build functions matching server signature
			buildFn := func(ctx context.Context, cfg *config.Config, workDir, output string) error {
				return buildOCIRootfs(ctx, cfg, workDir, output)
			}
			initramfsFn := func(ctx context.Context, cfg *config.Config, workDir, output string) error {
				return buildInitramfs(ctx, cfg, workDir, output)
			}

			return server.Start(ctx, opts, buildFn, initramfsFn)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "address to bind (default 127.0.0.1:7070 or FLEDGE_ADDR)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key required for requests (or FLEDGE_API_KEY)")
	cmd.Flags().StringVar(&cors, "cors-origins", "", "comma-separated allowed CORS origins (or FLEDGE_CORS_ORIGINS)")

	return cmd
}

type buildCLIOptions struct {
	ConfigPath      string
	OutputPath      string
	DockerfilePath  string
	ContextDir      string
	Target          string
	BuildArgs       []string
	OutputInitramfs bool
	ConfigExplicit  bool
}

func runBuild(opts buildCLIOptions) error {
	ctx, cancel := setupSignalHandling()
	defer cancel()

	if os.Geteuid() != 0 {
		logging.Error("Fledge requires root privileges for building artifacts")
		return fmt.Errorf("must run as root (use sudo)")
	}

	if opts.DockerfilePath != "" {
		return runDockerfileBuild(ctx, opts)
	}

	if opts.OutputInitramfs || opts.ContextDir != "" || opts.Target != "" || len(opts.BuildArgs) > 0 {
		return fmt.Errorf("--dockerfile is required when using --output-initramfs, --context, --target, or --build-arg")
	}

	return runConfigBuild(ctx, opts)
}

func runConfigBuild(ctx context.Context, opts buildCLIOptions) error {
	logging.Info("Starting Fledge build", "config", opts.ConfigPath)

	cfg, err := loadConfig(opts.ConfigPath)
	if err != nil {
		return err
	}

	output := determineOutputPath(cfg, opts.OutputPath)
	logging.Info("Output artifact", "path", output)

	workDir, err := getWorkingDirectory(opts.ConfigPath)
	if err != nil {
		return err
	}

	switch cfg.Strategy {
	case config.StrategyOCIRootfs:
		return buildOCIRootfs(ctx, cfg, workDir, output)
	case config.StrategyInitramfs:
		return buildInitramfs(ctx, cfg, workDir, output)
	default:
		return fmt.Errorf("unknown build strategy: %s", cfg.Strategy)
	}
}

func runDockerfileBuild(ctx context.Context, opts buildCLIOptions) error {
	if opts.ConfigExplicit {
		return fmt.Errorf("--config cannot be used when building directly from a Dockerfile")
	}

	dfPath := opts.DockerfilePath
	if dfPath == "" {
		return fmt.Errorf("dockerfile path is required")
	}

	dfAbs, err := filepath.Abs(dfPath)
	if err != nil {
		return fmt.Errorf("failed to resolve dockerfile path: %w", err)
	}

	info, err := os.Stat(dfAbs)
	if err != nil {
		return fmt.Errorf("failed to access dockerfile %s: %w", dfAbs, err)
	}
	if info.IsDir() {
		return fmt.Errorf("dockerfile path %s is a directory", dfAbs)
	}

	contextDir := opts.ContextDir
	if contextDir == "" {
		contextDir = filepath.Dir(dfAbs)
	} else if !filepath.IsAbs(contextDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to determine working directory: %w", err)
		}
		contextDir = filepath.Join(cwd, contextDir)
	}

	contextAbs, err := filepath.Abs(contextDir)
	if err != nil {
		return fmt.Errorf("failed to resolve context directory: %w", err)
	}

	ctxInfo, err := os.Stat(contextAbs)
	if err != nil {
		return fmt.Errorf("failed to access context directory %s: %w", contextAbs, err)
	}
	if !ctxInfo.IsDir() {
		return fmt.Errorf("context path %s is not a directory", contextAbs)
	}

	buildArgs, err := parseBuildArgs(opts.BuildArgs)
	if err != nil {
		return err
	}

	workDir := contextAbs
	dfForConfig := dfAbs
	if rel, err := filepath.Rel(workDir, dfAbs); err == nil {
		dfForConfig = rel
	}

	ctxForConfig := "."
	if rel, err := filepath.Rel(workDir, contextAbs); err == nil {
		ctxForConfig = rel
	} else {
		ctxForConfig = contextAbs
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = defaultDockerfileOutput(contextAbs, opts.OutputInitramfs)
	}

	strategy := config.StrategyOCIRootfs
	if opts.OutputInitramfs {
		strategy = config.StrategyInitramfs
	}

	cfg := &config.Config{
		Version:  "1",
		Strategy: strategy,
		Source: config.SourceConfig{
			Dockerfile: dfForConfig,
			Context:    ctxForConfig,
			Target:     opts.Target,
			BuildArgs:  buildArgs,
		},
	}

	cfg.Agent = config.DefaultAgentConfig()
	if strategy == config.StrategyOCIRootfs {
		cfg.Filesystem = config.DefaultFilesystemConfig()
	} else {
		cfg.Source.BusyboxURL = config.DefaultBusyboxURL
		cfg.Source.BusyboxSHA256 = config.DefaultBusyboxSHA256
	}

	logging.Info("Starting Dockerfile build",
		"dockerfile", dfAbs,
		"context", contextAbs,
		"output", outputPath,
		"format", strategy)

	if strategy == config.StrategyOCIRootfs {
		return buildOCIRootfs(ctx, cfg, workDir, outputPath)
	}
	return buildInitramfs(ctx, cfg, workDir, outputPath)
}

func parseBuildArgs(args []string) (map[string]string, error) {
	if len(args) == 0 {
		return nil, nil
	}

	result := make(map[string]string, len(args))
	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --build-arg %q: must be in KEY=VALUE form", arg)
		}

		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("invalid --build-arg %q: key cannot be empty", arg)
		}

		result[key] = parts[1]
	}

	return result, nil
}

func defaultDockerfileOutput(contextDir string, initramfs bool) string {
	base := filepath.Base(contextDir)
	if base == "." || base == string(filepath.Separator) {
		base = "plugin"
	}

	sanitized := sanitizeFilename(base)
	if sanitized == "" {
		sanitized = "plugin"
	}

	if initramfs {
		return sanitized + ".cpio.gz"
	}
	return sanitized + ".img"
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
