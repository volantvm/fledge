package builder

import (
	"context"
	"errors"
	"sync"
)

type DockerfileBuildInput struct {
	Dockerfile string
	ContextDir string
	Target     string
	BuildArgs  map[string]string
	DestDir    string
}

type DockerfileBuildFunc func(ctx context.Context, input DockerfileBuildInput) error

var (
	dockerfileBuilderMu sync.RWMutex
	dockerfileBuilder   DockerfileBuildFunc
)

func RegisterDockerfileBuilder(fn DockerfileBuildFunc) {
	dockerfileBuilderMu.Lock()
	defer dockerfileBuilderMu.Unlock()
	dockerfileBuilder = fn
}

func invokeDockerfileBuilder(ctx context.Context, input DockerfileBuildInput) error {
	dockerfileBuilderMu.RLock()
	fn := dockerfileBuilder
	dockerfileBuilderMu.RUnlock()

	if fn == nil {
		return errors.New("initramfs builder: Dockerfile builds require embedded BuildKit support")
	}

	return fn(ctx, input)
}
