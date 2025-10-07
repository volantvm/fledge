//go:build !linux

package embedded

import (
    "context"
    "fmt"
)

func BuildDockerfileToRootfs(ctx context.Context, dockerfile, contextDir, target string, buildArgs map[string]string, destDir string) error {
    return fmt.Errorf("embedded buildkit: unsupported platform (requires linux)")
}
