//go:build linux

package launcher

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
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
	InitramfsPath string // optional initramfs archive supplied via --initramfs
	TapDevice     string // host tap interface to attach to the VM
	MACAddress    string // optional guest MAC address override
	IPAddress     string // optional guest IP address hint for Cloud Hypervisor
	Gateway       string // optional gateway (used in kernel args)
	Netmask       string // optional netmask hint for Cloud Hypervisor
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

func (i *chInstance) PID() int {
	if i.cmd != nil && i.cmd.Process != nil {
		return i.cmd.Process.Pid
	}
	return 0
}

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
	if l.Bin == "" {
		l.Bin = "cloud-hypervisor"
	}
	if spec.CPUCores <= 0 {
		spec.CPUCores = 2
	}
	if spec.MemoryMB <= 0 {
		spec.MemoryMB = 1024
	}

	// Ensure runtime/log directories exist
	if l.RuntimeDir == "" {
		l.RuntimeDir = filepath.Join(os.TempDir(), "fledge-vm")
	}
	if err := os.MkdirAll(l.RuntimeDir, 0o755); err != nil {
		return nil, fmt.Errorf("runtime dir: %w", err)
	}
	if l.LogDir == "" {
		l.LogDir = l.RuntimeDir
	}
	if err := os.MkdirAll(l.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("log dir: %w", err)
	}

	// Choose kernel
	kernel := spec.KernelPath
	if kernel == "" {
		if l.KernelBZImage != "" {
			kernel = l.KernelBZImage
		} else {
			kernel = l.KernelVMLinux
		}
	}
	if kernel == "" {
		return nil, fmt.Errorf("no kernel path configured (set FLEDGE_KERNEL_BZIMAGE or FLEDGE_KERNEL_VMLINUX)")
	}

	// Default cmdline
	cmdline := []string{"console=ttyS0", "panic=1", "rootwait"}
	if spec.DiskPath != "" {
		// Detect filesystem type from file extension
		fsType := "ext4" // default for legacy .img files
		overlaySize := ""
		
		if strings.HasSuffix(spec.DiskPath, ".squashfs") {
			fsType = "squashfs"
			// Default overlay size 1G, can be overridden via kernel args
			overlaySize = "1G"
		} else if strings.HasSuffix(spec.DiskPath, ".xfs") {
			fsType = "xfs"
		} else if strings.HasSuffix(spec.DiskPath, ".btrfs") {
			fsType = "btrfs"
		}
		
		// Add root and filesystem type
		cmdline = append(cmdline, "root=/dev/vda", "rootfstype="+fsType)
		
		// For squashfs, it's read-only at lower layer, writable via overlayfs
		// For others, add rw flag
		if fsType != "squashfs" {
			cmdline = append(cmdline, "rw")
		} else if overlaySize != "" {
			cmdline = append(cmdline, "overlay_size="+overlaySize)
		}
	}
	if extra := strings.TrimSpace(spec.KernelArgs); extra != "" {
		cmdline = append(cmdline, strings.Fields(extra)...)
	}

	cmdlineArg := strings.Join(cmdline, " ")

	args := []string{
		"--cpus", "boot=" + strconv.Itoa(spec.CPUCores),
		"--memory", fmt.Sprintf("size=%dM", spec.MemoryMB),
		"--kernel", kernel,
		"--cmdline", cmdlineArg,
	}
	if spec.DiskPath != "" {
		ro := "off"
		if spec.ReadOnlyRoot {
			ro = "on"
		}
		args = append(args, "--disk", fmt.Sprintf("path=%s,readonly=%s", spec.DiskPath, ro))
	}

	if spec.InitramfsPath != "" {
		initramfs := spec.InitramfsPath
		if !filepath.IsAbs(initramfs) {
			abs, err := filepath.Abs(initramfs)
			if err != nil {
				return nil, fmt.Errorf("resolve initramfs path: %w", err)
			}
			initramfs = abs
		}
		fi, err := os.Stat(initramfs)
		if err != nil {
			return nil, fmt.Errorf("initramfs path: %w", err)
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("initramfs path: is a directory")
		}
		args = append(args, "--initramfs", initramfs)
	}

	if spec.TapDevice != "" {
		netParts := []string{fmt.Sprintf("tap=%s", spec.TapDevice)}
		mac := spec.MACAddress
		if mac == "" {
			var err error
			mac, err = generateLocalMAC()
			if err != nil {
				return nil, fmt.Errorf("tap mac: %w", err)
			}
		} else {
			if _, err := net.ParseMAC(mac); err != nil {
				return nil, fmt.Errorf("tap mac: %w", err)
			}
		}
		netParts = append(netParts, fmt.Sprintf("mac=%s", mac))
		if ip := strings.TrimSpace(spec.IPAddress); ip != "" {
			netParts = append(netParts, fmt.Sprintf("ip=%s", ip))
		}
		if mask := strings.TrimSpace(spec.Netmask); mask != "" {
			netParts = append(netParts, fmt.Sprintf("mask=%s", mask))
		}
		args = append(args, "--net", strings.Join(netParts, ","))
	}

	// Serial to file per-VM
	if spec.Name == "" {
		spec.Name = "vm"
	}
	serialLog := filepath.Join(l.LogDir, spec.Name+"-serial.log")
	args = append(args, "--serial", "file="+serialLog)

	cmd := exec.CommandContext(ctx, l.Bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("launch cloud-hypervisor: %w", err)
	}
	return &chInstance{name: spec.Name, cmd: cmd}, nil
}

func generateLocalMAC() (string, error) {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	buf[0] = (buf[0] | 0x02) & 0xFE
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", buf[0], buf[1], buf[2], buf[3], buf[4], buf[5]), nil
}

// RandomMAC returns a locally administered unicast MAC address suitable for guests.
func RandomMAC() (string, error) {
	return generateLocalMAC()
}
