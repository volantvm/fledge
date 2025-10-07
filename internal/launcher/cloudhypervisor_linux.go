//go:build linux

package launcher

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"
)

// LaunchSpec describes a minimal VM configuration for Cloud Hypervisor.
type LaunchSpec struct {
    Name          string
    CPUCores      int
    MemoryMB      int
    KernelArgs    string // appended to default cmdline
    KernelPath    string // optional override; if empty, defaults from Launcher
    DiskPath      string // path to rootfs image (virtio-blk)
    ReadOnlyRoot  bool
}

// Instance represents a running VM process.
type Instance interface {
    PID() int
    Wait(ctx context.Context) error
    Stop(ctx context.Context) error
}

// Launcher provides a minimal Cloud Hypervisor process launcher.
type Launcher struct {
    Bin           string
    KernelBZImage string
    KernelVMLinux string
    RuntimeDir    string
    LogDir        string
}

// New constructs a new Launcher.
func New(bin, bzImage, vmlinux, runtimeDir, logDir string) *Launcher {
    return &Launcher{Bin: bin, KernelBZImage: bzImage, KernelVMLinux: vmlinux, RuntimeDir: runtimeDir, LogDir: logDir}
}

type chInstance struct {
    name string
    cmd  *exec.Cmd
}

func (i *chInstance) PID() int { if i.cmd != nil && i.cmd.Process != nil { return i.cmd.Process.Pid }; return 0 }

func (i *chInstance) Wait(ctx context.Context) error {
    done := make(chan error, 1)
    go func() { done <- i.cmd.Wait() }()
    select {
    case <-ctx.Done():
        return ctx.Err()
    case err := <-done:
        return err
    }
}

func (i *chInstance) Stop(ctx context.Context) error {
    if i.cmd == nil || i.cmd.Process == nil {
        return nil
    }
    // Attempt graceful shutdown then SIGKILL
    _ = i.cmd.Process.Signal(syscall.SIGTERM)
    done := make(chan error, 1)
    go func() { done <- i.cmd.Wait() }()
    select {
    case <-ctx.Done():
        _ = i.cmd.Process.Kill()
        return ctx.Err()
    case err := <-done:
        return err
    }
}

// Launch starts a Cloud Hypervisor VM process.
func (l *Launcher) Launch(ctx context.Context, spec LaunchSpec) (Instance, error) {
    if l.Bin == "" { l.Bin = "cloud-hypervisor" }
    if spec.CPUCores <= 0 { spec.CPUCores = 2 }
    if spec.MemoryMB <= 0 { spec.MemoryMB = 1024 }

    // Ensure runtime/log directories exist
    if l.RuntimeDir == "" { l.RuntimeDir = filepath.Join(os.TempDir(), "fledge-vm") }
    if err := os.MkdirAll(l.RuntimeDir, 0o755); err != nil { return nil, fmt.Errorf("runtime dir: %w", err) }
    if l.LogDir == "" { l.LogDir = l.RuntimeDir }
    if err := os.MkdirAll(l.LogDir, 0o755); err != nil { return nil, fmt.Errorf("log dir: %w", err) }

    // Choose kernel
    kernel := spec.KernelPath
    if kernel == "" {
        if l.KernelBZImage != "" { kernel = l.KernelBZImage } else { kernel = l.KernelVMLinux }
    }
    if kernel == "" {
        return nil, fmt.Errorf("no kernel path configured (set FLEDGE_KERNEL_BZIMAGE or FLEDGE_KERNEL_VMLINUX)")
    }

    // Default cmdline
    cmdline := []string{ "console=hvc0", "panic=1", "pci=off" }
    if spec.DiskPath != "" {
        // Use virtio-blk as vda
        cmdline = append(cmdline, "root=/dev/vda", "rw")
    }
    if strings.TrimSpace(spec.KernelArgs) != "" {
        cmdline = append(cmdline, spec.KernelArgs)
    }

    args := []string{
        "--cpus", "boot=" + strconv.Itoa(spec.CPUCores),
        "--memory", fmt.Sprintf("size=%dM", spec.MemoryMB),
        "--kernel", kernel,
        "--cmdline", strings.Join(cmdline, " "),
    }
    if spec.DiskPath != "" {
        ro := "off"
        if spec.ReadOnlyRoot { ro = "on" }
        args = append(args, "--disk", fmt.Sprintf("path=%s,readonly=%s", spec.DiskPath, ro))
    }

    // Serial to file per-VM
    if spec.Name == "" { spec.Name = "vm" }
    serialLog := filepath.Join(l.LogDir, spec.Name+"-serial.log")
    args = append(args, "--serial", "file="+serialLog)

    cmd := exec.CommandContext(ctx, l.Bin, args...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    if err := cmd.Start(); err != nil { return nil, fmt.Errorf("launch cloud-hypervisor: %w", err) }
    return &chInstance{name: spec.Name, cmd: cmd}, nil
}
