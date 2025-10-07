//go:build linux

package microvmworker

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    ch "github.com/volantvm/volant/internal/server/orchestrator/cloudhypervisor"
    "github.com/volantvm/volant/internal/server/orchestrator/runtime"
)

// Worker is a skeleton for a BuildKit worker that executes steps inside
// Cloud Hypervisor microVMs.
type Worker struct {
    Launcher      runtime.Launcher
    RuntimeDir    string
    KernelBZImage string
    KernelVMLinux string
}

// NewFromEnv constructs a Worker using environment variables for configuration.
// FLEDGE_KERNEL_BZIMAGE and FLEDGE_KERNEL_VMLINUX can override default kernel paths.
// CLOUDHYPERVISOR points to the cloud-hypervisor binary (defaults to "cloud-hypervisor").
func NewFromEnv(runtimeDir string) (*Worker, error) {
    if runtimeDir == "" {
        runtimeDir = filepath.Join(os.TempDir(), "fledge-microvm")
    }
    if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
        return nil, fmt.Errorf("microvmworker: ensure runtime dir: %w", err)
    }
    bzImage := os.Getenv("FLEDGE_KERNEL_BZIMAGE")
    if bzImage == "" {
        bzImage = "/var/lib/volant/kernel/bzImage"
    }
    vmlinux := os.Getenv("FLEDGE_KERNEL_VMLINUX")
    if vmlinux == "" {
        vmlinux = "/var/lib/volant/kernel/vmlinux"
    }
    bin := os.Getenv("CLOUDHYPERVISOR")
    if bin == "" {
        bin = "cloud-hypervisor"
    }

    launcher := ch.New(bin, bzImage, vmlinux, runtimeDir, runtimeDir)
    return &Worker{
        Launcher:      launcher,
        RuntimeDir:    runtimeDir,
        KernelBZImage: bzImage,
        KernelVMLinux: vmlinux,
    }, nil
}

// BootVM boots a minimal microVM for executing build steps.
// This is a skeleton; the actual worker will prepare a base rootfs and expose
// a mechanism to run commands and capture filesystem diffs between steps.
func (w *Worker) BootVM(ctx context.Context, name string, spec runtime.LaunchSpec) (runtime.Instance, error) {
    if w.Launcher == nil {
        return nil, fmt.Errorf("microvmworker: launcher not configured")
    }
    if spec.Name == "" {
        spec.Name = name
    }
    if spec.CPUCores == 0 {
        spec.CPUCores = 2
    }
    if spec.MemoryMB == 0 {
        spec.MemoryMB = 1024
    }
    return w.Launcher.Launch(ctx, spec)
}
