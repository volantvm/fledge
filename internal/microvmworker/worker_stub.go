//go:build !linux

package microvmworker

import (
	"context"
	"fmt"
)

type Worker struct{}

func NewFromEnv(runtimeDir string) (*Worker, error) {
	return nil, fmt.Errorf("microvmworker: unsupported platform (requires linux)")
}

func (w *Worker) BootVM(ctx context.Context, name string, spec any) (any, error) {
	return nil, fmt.Errorf("microvmworker: unsupported platform (requires linux)")
}

func (w *Worker) NewBuildkitWorker(ctx context.Context, root string, hosts any) (any, error) {
	return nil, fmt.Errorf("microvmworker: unsupported platform (requires linux)")
}
