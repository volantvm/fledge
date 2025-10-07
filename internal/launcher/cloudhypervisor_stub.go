//go:build !linux

package launcher

import (
    "context"
    "fmt"
)

type LaunchSpec struct{}

type Instance interface{
    PID() int
    Wait(ctx context.Context) error
    Stop(ctx context.Context) error
}

type Launcher struct{}

func New(bin, bzImage, vmlinux, runtimeDir, logDir string) *Launcher { return &Launcher{} }

func (l *Launcher) Launch(ctx context.Context, spec LaunchSpec) (Instance, error) {
    return nil, fmt.Errorf("cloud-hypervisor launcher: unsupported platform (requires linux)")
}
