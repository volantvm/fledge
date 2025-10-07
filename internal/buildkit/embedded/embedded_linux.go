//go:build linux

package embedded

import (
    "context"
    "fmt"
)

// BuildDockerfileToRootfs is a Linux-only stub for the embedded BuildKit path.
// It will be implemented to execute Dockerfile builds using an in-process BuildKit
// solver with a custom microVM worker.
func BuildDockerfileToRootfs(ctx context.Context, dockerfile, contextDir, target string, buildArgs map[string]string, destDir string) error {
    return fmt.Errorf("embedded buildkit: not implemented yet")
}
