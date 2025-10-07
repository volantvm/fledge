package buildkit

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    bkclient "github.com/moby/buildkit/client"
    embedded "github.com/volantvm/fledge/internal/buildkit/embedded"
)

// Options for building a Dockerfile to a local rootfs directory using BuildKit.
type DockerfileBuildOptions struct {
	// Address to connect to buildkitd, e.g. "unix:///run/buildkit/buildkitd.sock"
	Address string

	// Absolute path to the Dockerfile
	Dockerfile string

	// Build context directory (absolute path)
	ContextDir string

	// Build target stage (optional)
	Target string

	// Build-arg key/value map
	BuildArgs map[string]string

	// Destination directory to export the built rootfs (will be created if not exists)
	DestDir string
}

// BuildDockerfileToRootfs uses BuildKit's dockerfile.v0 frontend to build the given Dockerfile
// and exports the result to a local directory containing the built root filesystem.
func BuildDockerfileToRootfs(ctx context.Context, opts DockerfileBuildOptions) error {
    // Embedded is now the default unless explicitly set to daemon/external
    mode := strings.ToLower(strings.TrimSpace(os.Getenv("FLEDGE_BUILDKIT_MODE")))
    if mode == "" || mode == "embedded" {
        return embedded.BuildDockerfileToRootfs(ctx, opts.Dockerfile, opts.ContextDir, opts.Target, opts.BuildArgs, opts.DestDir)
    }

    addr := opts.Address
    if addr == "" {
        addr = DefaultAddress()
    }

	if err := os.MkdirAll(opts.DestDir, 0o755); err != nil {
		return fmt.Errorf("failed to create dest dir: %w", err)
	}

	// Connect to buildkitd
	c, err := bkclient.New(ctx, addr)
	if err != nil {
		return fmt.Errorf("buildkit connect failed: %w", err)
	}
	defer c.Close()

	// dockerfile.v0 frontend expects local dirs named "context" and "dockerfile"
	// "filename" is the dockerfile path relative to dockerfile local dir.
	dfDir := filepath.Dir(opts.Dockerfile)
	dfBase := filepath.Base(opts.Dockerfile)

	frontendAttrs := map[string]string{
		"filename": dfBase,
	}
	if opts.Target != "" {
		frontendAttrs["target"] = opts.Target
	}
	for k, v := range opts.BuildArgs {
		frontendAttrs["build-arg:"+k] = v
	}

	solveOpt := bkclient.SolveOpt{
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
		LocalDirs: map[string]string{
			"context":   opts.ContextDir,
			"dockerfile": dfDir,
		},
		Exports: []bkclient.ExportEntry{
			{
				Type:      bkclient.ExporterLocal,
				OutputDir: opts.DestDir,
			},
		},
	}

	_, err = c.Solve(ctx, nil, solveOpt, nil)
	if err != nil {
		return fmt.Errorf("buildkit solve failed: %w", err)
	}
	return nil
}

// Compose minimal schema (subset) for build configuration
type ComposeFile struct {
	Services map[string]ComposeService `yaml:"services"`
}

type ComposeService struct {
	Build *ComposeBuild `yaml:"build"`
}

type ComposeBuild struct {
	Context    string            `yaml:"context"`
	Dockerfile string            `yaml:"dockerfile"`
	Target     string            `yaml:"target"`
	Args       map[string]string `yaml:"args"`
}

// DefaultAddress reads FLEDGE_BUILDKIT_ADDR or returns a sensible default.
func DefaultAddress() string {
	if v := os.Getenv("FLEDGE_BUILDKIT_ADDR"); v != "" {
		return v
	}
	// Common rootless buildkitd socket location
	return "unix:///run/buildkit/buildkitd.sock"
}
