//go:build linux

package microvmworker

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/mount"
	"github.com/moby/buildkit/executor"
	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/volantvm/fledge/internal/builder"
	"github.com/volantvm/fledge/internal/config"
	ch "github.com/volantvm/fledge/internal/launcher"
	"github.com/volantvm/fledge/internal/logging"
	"github.com/volantvm/fledge/internal/utils"
)

// Executor runs BuildKit exec steps inside Cloud Hypervisor microVMs.
type Executor struct {
	worker     *Worker
	workspace  string
	supportDir string

	tempMu        sync.Mutex
	nextVMID      int
	busyboxMu     sync.Mutex
	busyboxPath   string
	agentStubMu   sync.Mutex
	agentStubPath string

	baseKernel string
}

// NewExecutor creates a microVM-backed BuildKit executor.
func NewExecutor(w *Worker) (*Executor, error) {
	if w == nil {
		return nil, fmt.Errorf("microvm executor: worker is nil")
	}
	workspace := filepath.Join(w.RuntimeDir, "executor")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, fmt.Errorf("microvm executor: prepare workspace: %w", err)
	}

	supportDir := filepath.Join(workspace, "support")
	if err := os.MkdirAll(supportDir, 0o755); err != nil {
		return nil, fmt.Errorf("microvm executor: prepare support dir: %w", err)
	}

	return &Executor{
		worker:     w,
		workspace:  workspace,
		supportDir: supportDir,
		baseKernel: "init=/.fledge/init reboot=k",
	}, nil
}

// Run implements executor.Executor by staging the rootfs onto an ext4 disk image,
// launching a Cloud Hypervisor microVM, executing the requested process, and
// propagating filesystem changes back into the snapshot.
func (e *Executor) Run(ctx context.Context, id string, root executor.Mount, mounts []executor.Mount, process executor.ProcessInfo, started chan<- struct{}) (resourcestypes.Recorder, error) {
	if e.worker == nil {
		return nil, fmt.Errorf("microvm executor: worker not configured")
	}
	if len(process.Meta.Args) == 0 {
		return nil, fmt.Errorf("microvm executor: no command provided")
	}

	rootDir, rootCleanup, err := e.mountSnapshot(ctx, root)
	if err != nil {
		return nil, err
	}
	defer rootCleanup()

	if err := e.applyAdditionalMounts(ctx, rootDir, mounts); err != nil {
		return nil, err
	}

	imagePath, err := e.prepareDiskImage(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	defer os.Remove(imagePath)

	if err := e.populateDisk(ctx, imagePath, rootDir, process); err != nil {
		return nil, err
	}

	vmName := e.allocateVMName(id)
	initramfsPath, initramfsCleanup, err := e.buildInitramfs(ctx, vmName)
	if err != nil {
		return nil, err
	}
	defer initramfsCleanup()

	inst, err := e.worker.BootVM(ctx, vmName, ch.LaunchSpec{
		Name:          vmName,
		CPUCores:      2,
		MemoryMB:      1536,
		KernelArgs:    e.baseKernel,
		DiskPath:      imagePath,
		ReadOnlyRoot:  false,
		InitramfsPath: initramfsPath,
		UseSlirp:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("microvm executor: launch vm: %w", err)
	}

	if started != nil {
		close(started)
	}

	waitErr := inst.Wait(ctx)

	stdoutBuf, stderrBuf, exitCode, err := e.collectResults(ctx, imagePath, rootDir, process)
	if err != nil {
		return nil, err
	}

	if process.Stdout != nil && stdoutBuf != nil {
		_, _ = io.Copy(process.Stdout, bytes.NewReader(stdoutBuf))
	}
	if process.Stderr != nil && stderrBuf != nil {
		_, _ = io.Copy(process.Stderr, bytes.NewReader(stderrBuf))
	}

	if exitCode < 0 {
		logging.Warn("microvm executor: guest exit code not captured", "vm", vmName)
		if waitErr != nil {
			return nil, fmt.Errorf("microvm executor: vm wait: %w", waitErr)
		}
		return nil, fmt.Errorf("microvm executor: guest exit code missing (see previous warnings)")
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitCode >= 0 {
			// rely on exit code captured from guest
		} else {
			return nil, fmt.Errorf("microvm executor: vm wait: %w", waitErr)
		}
	}

	if exitCode != 0 {
		return nil, &gatewayapi.ExitError{ExitCode: uint32(exitCode)}
	}

	return nil, nil
}

// Exec is not supported for microVM executor; each Run creates an isolated VM.
func (e *Executor) Exec(ctx context.Context, id string, process executor.ProcessInfo) error {
	return fmt.Errorf("microvm executor: Exec not supported")
}

func (e *Executor) mountSnapshot(ctx context.Context, mnt executor.Mount) (string, func() error, error) {
	mref, err := mnt.Src.Mount(ctx, mnt.Readonly)
	if err != nil {
		return "", nil, fmt.Errorf("microvm executor: mount root: %w", err)
	}

	mounts, release, err := mref.Mount()
	if err != nil {
		return "", nil, fmt.Errorf("microvm executor: resolve root mounts: %w", err)
	}

	rootDir, err := os.MkdirTemp(e.workspace, "root-*")
	if err != nil {
		release()
		return "", nil, fmt.Errorf("microvm executor: create root tempdir: %w", err)
	}

	if err := mount.All(mounts, rootDir); err != nil {
		release()
		return "", nil, fmt.Errorf("microvm executor: mount rootfs: %w", err)
	}

	cleanup := func() error {
		var firstErr error
		if err := mount.Unmount(rootDir, 0); err != nil {
			firstErr = err
		}
		if err := release(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := os.RemoveAll(rootDir); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}

	return rootDir, cleanup, nil
}

func (e *Executor) applyAdditionalMounts(ctx context.Context, rootDir string, mounts []executor.Mount) error {
	for _, m := range mounts {
		logging.Warn("microvm executor: ignoring unsupported mount", "dest", m.Dest)
	}
	return nil
}

func (e *Executor) prepareDiskImage(ctx context.Context, rootDir string) (string, error) {
	usage, err := dirSize(rootDir)
	if err != nil {
		return "", fmt.Errorf("microvm executor: size rootfs: %w", err)
	}
	if usage <= 0 {
		usage = 1 << 20
	}

	overhead := usage / 2
	if overhead < 512<<20 {
		overhead = 512 << 20
	}

	total := usage + overhead
	if total < 512<<20 {
		total = 512 << 20
	}
	const align = 64 << 20
	if rem := total % align; rem != 0 {
		total += align - rem
	}

	imagePath := filepath.Join(e.workspace, fmt.Sprintf("disk-%d.img", time.Now().UnixNano()))
	file, err := os.Create(imagePath)
	if err != nil {
		return "", fmt.Errorf("microvm executor: create disk image: %w", err)
	}
	if err := file.Truncate(total); err != nil {
		file.Close()
		return "", fmt.Errorf("microvm executor: truncate disk: %w", err)
	}
	file.Close()

	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", "-m", "0", "-E", "lazy_itable_init=0,lazy_journal_init=0", imagePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("microvm executor: mkfs.ext4: %w output=%s", err, string(output))
	}

	return imagePath, nil
}

func (e *Executor) populateDisk(ctx context.Context, imagePath, rootDir string, process executor.ProcessInfo) error {
	return e.withDiskMount(ctx, imagePath, func(mountPoint string) error {
		if err := clearDir(mountPoint); err != nil {
			return fmt.Errorf("clear mount: %w", err)
		}
		if err := copyTree(rootDir, mountPoint); err != nil {
			return fmt.Errorf("copy rootfs: %w", err)
		}
		return e.writeInitFiles(ctx, mountPoint, process)
	})
}

func (e *Executor) collectResults(ctx context.Context, imagePath, rootDir string, process executor.ProcessInfo) ([]byte, []byte, int, error) {
	var stdoutBuf, stderrBuf []byte
	exitCode := -1

	err := e.withDiskMount(ctx, imagePath, func(mountPoint string) error {
		ctrlDir := filepath.Join(mountPoint, ".fledge")
		stdoutBuf, _ = os.ReadFile(filepath.Join(ctrlDir, "stdout"))
		stderrBuf, _ = os.ReadFile(filepath.Join(ctrlDir, "stderr"))
		exitPath := filepath.Join(ctrlDir, "exit_code")
		if data, err := os.ReadFile(exitPath); err == nil {
			exitStr := strings.TrimSpace(string(data))
			if exitStr == "" {
				logging.Warn("microvm executor: exit code file empty", "path", exitPath)
			} else if v, parseErr := strconv.Atoi(exitStr); parseErr != nil {
				logging.Warn("microvm executor: parse exit code", "path", exitPath, "value", exitStr, "error", parseErr)
			} else {
				exitCode = v
			}
		} else {
			if !errors.Is(err, os.ErrNotExist) {
				logging.Warn("microvm executor: read exit code", "path", exitPath, "error", err)
			}
		}

		_ = os.RemoveAll(ctrlDir)

		if err := replaceDirContents(rootDir, mountPoint); err != nil {
			return fmt.Errorf("sync rootfs: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, exitCode, err
	}

	return stdoutBuf, stderrBuf, exitCode, nil
}

func (e *Executor) withDiskMount(ctx context.Context, imagePath string, fn func(mountPoint string) error) error {
	loopDev, err := attachLoop(imagePath)
	if err != nil {
		return err
	}
	defer detachLoop(loopDev)

	mountPoint, err := os.MkdirTemp(e.workspace, "mnt-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mountPoint)

	cmd := exec.CommandContext(ctx, "mount", loopDev, mountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("microvm executor: mount disk: %w output=%s", err, string(output))
	}
	defer func() {
		cmd := exec.Command("umount", mountPoint)
		if output, err := cmd.CombinedOutput(); err != nil {
			logging.Warn("microvm executor: umount disk", "error", err, "output", string(output))
		}
	}()

	return fn(mountPoint)
}

func (e *Executor) writeInitFiles(ctx context.Context, mountPoint string, process executor.ProcessInfo) error {
	controlDir := filepath.Join(mountPoint, ".fledge")
	if err := os.MkdirAll(controlDir, 0o755); err != nil {
		return err
	}

	if err := e.installSupportBinaries(ctx, mountPoint, controlDir); err != nil {
		return err
	}

	initPath := filepath.Join(controlDir, "init")
	script := buildInitScript(process)
	if err := os.WriteFile(initPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write init script: %w", err)
	}

	volantInit := filepath.Join(mountPoint, ".volant_init")
	if err := os.WriteFile(volantInit, []byte("/.fledge/init\n"), 0o644); err != nil {
		return fmt.Errorf("write .volant_init: %w", err)
	}

	if err := e.ensureKestrelShim(mountPoint); err != nil {
		return err
	}

	for _, name := range []string{"stdout", "stderr"} {
		path := filepath.Join(controlDir, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				return err
			}
		}
	}

	return nil
}

func (e *Executor) ensureKestrelShim(mountPoint string) error {
	kestrelPath := filepath.Join(mountPoint, "bin", "kestrel")
	target := "/.fledge/init"

	info, err := os.Lstat(kestrelPath)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			if current, readErr := os.Readlink(kestrelPath); readErr == nil && current == target {
				return nil
			}
		}
		backupPath := kestrelPath + ".orig"
		if removeErr := os.Remove(backupPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("microvm executor: remove stale kestrel backup: %w", removeErr)
		}
		if err := os.Rename(kestrelPath, backupPath); err != nil {
			return fmt.Errorf("microvm executor: backup existing kestrel binary: %w", err)
		}
		logging.Warn("microvm executor: replacing guest kestrel binary with build init shim", "original", kestrelPath, "backup", backupPath)
	case errors.Is(err, os.ErrNotExist):
		// Nothing to back up
	default:
		return fmt.Errorf("microvm executor: inspect kestrel binary: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(kestrelPath), 0o755); err != nil {
		return fmt.Errorf("microvm executor: ensure /bin directory: %w", err)
	}
	if err := os.Symlink(target, kestrelPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			if removeErr := os.Remove(kestrelPath); removeErr != nil {
				return fmt.Errorf("microvm executor: replace existing kestrel shim: %w", removeErr)
			}
			if err := os.Symlink(target, kestrelPath); err != nil {
				return fmt.Errorf("microvm executor: relink kestrel shim: %w", err)
			}
			return nil
		}
		return fmt.Errorf("microvm executor: link kestrel shim: %w", err)
	}
	return nil
}

func (e *Executor) installSupportBinaries(ctx context.Context, mountPoint, controlDir string) error {
	binDir := filepath.Join(controlDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("microvm executor: create support bin dir: %w", err)
	}

	busyboxHostPath, err := e.ensureBusybox(ctx)
	if err != nil {
		return err
	}

	busyboxTarget := filepath.Join(binDir, "busybox")
	if err := copyFile(busyboxHostPath, busyboxTarget, 0o755); err != nil {
		return fmt.Errorf("microvm executor: stage busybox: %w", err)
	}

	for _, applet := range []string{"sh", "ip", "udhcpc"} {
		if err := ensureSymlink(filepath.Join(binDir, applet), "busybox"); err != nil {
			return fmt.Errorf("microvm executor: link busybox %s: %w", applet, err)
		}
	}
	udhcpcScript := filepath.Join(binDir, "udhcpc-script")
	if err := os.WriteFile(udhcpcScript, []byte(buildUDHCPCScript()), 0o755); err != nil {
		return fmt.Errorf("microvm executor: write udhcpc script: %w", err)
	}

	rootShell := filepath.Join(mountPoint, "bin", "sh")
	if info, err := os.Stat(rootShell); err == nil {
		if info.Mode()&0o111 == 0 {
			logging.Warn("microvm executor: /bin/sh exists but is not executable", "path", rootShell)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(rootShell), 0o755); err != nil {
			return fmt.Errorf("microvm executor: create /bin directory: %w", err)
		}
		if err := os.Symlink("/.fledge/bin/busybox", rootShell); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("microvm executor: link /bin/sh: %w", err)
		}
	} else {
		return fmt.Errorf("microvm executor: stat /bin/sh: %w", err)
	}

	return nil
}

func (e *Executor) buildInitramfs(ctx context.Context, vmName string) (string, func(), error) {
	busyboxHostPath, err := e.ensureBusybox(ctx)
	if err != nil {
		return "", func() {}, fmt.Errorf("microvm executor: prepare busybox for initramfs: %w", err)
	}

	agentStubPath, err := e.ensureAgentStub()
	if err != nil {
		return "", func() {}, err
	}

	cfg := &config.Config{
		Version:  "1",
		Strategy: config.StrategyInitramfs,
		Agent: &config.AgentConfig{
			SourceStrategy: config.AgentSourceLocal,
			Path:           agentStubPath,
		},
		Source: config.SourceConfig{
			BusyboxURL:    config.DefaultBusyboxURL,
			BusyboxSHA256: config.DefaultBusyboxSHA256,
		},
	}

	if err := config.Validate(cfg); err != nil {
		return "", func() {}, fmt.Errorf("microvm executor: initramfs config invalid: %w", err)
	}

	outputPath := filepath.Join(e.supportDir, fmt.Sprintf("initramfs-%s-%d.cpio.gz", vmName, time.Now().UnixNano()))
	b := builder.NewInitramfsBuilder(cfg, e.supportDir, outputPath)
	b.BusyboxLocalPath = busyboxHostPath

	if err := b.Build(); err != nil {
		os.Remove(outputPath)
		return "", func() {}, fmt.Errorf("microvm executor: build initramfs: %w", err)
	}

	cleanup := func() {
		if err := os.Remove(outputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logging.Warn("microvm executor: cleanup initramfs", "path", outputPath, "error", err)
		}
	}

	return outputPath, cleanup, nil
}

// ensureAgentStub provides a lightweight kestrel replacement so the initramfs
// builder can satisfy default-mode requirements without downloading releases.
func (e *Executor) ensureAgentStub() (string, error) {
	e.agentStubMu.Lock()
	defer e.agentStubMu.Unlock()

	if e.agentStubPath != "" {
		if _, err := os.Stat(e.agentStubPath); err == nil {
			return e.agentStubPath, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("microvm executor: stat agent stub: %w", err)
		}
	}

	stubPath := filepath.Join(e.supportDir, "kestrel-stub.sh")
	script := `#!/bin/sh
echo "microvm executor: kestrel fallback stub invoked" >&2
exec /bin/sh "$@"
`
	if err := os.WriteFile(stubPath, []byte(script), 0o755); err != nil {
		return "", fmt.Errorf("microvm executor: write agent stub: %w", err)
	}

	e.agentStubPath = stubPath
	return stubPath, nil
}

func (e *Executor) ensureBusybox(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	e.busyboxMu.Lock()
	defer e.busyboxMu.Unlock()

	target := filepath.Join(e.supportDir, "busybox")

	localPath, err := locateLocalBusybox()
	if err != nil {
		return "", fmt.Errorf("microvm executor: locate local busybox: %w", err)
	}
	if localPath != "" {
		logging.Info("microvm executor: staging busybox from host", "path", localPath)
		if err := copyFile(localPath, target, 0o755); err != nil {
			return "", fmt.Errorf("microvm executor: stage busybox from host: %w", err)
		}
		if err := os.Chmod(target, 0o755); err != nil {
			return "", fmt.Errorf("microvm executor: chmod busybox: %w", err)
		}
		e.busyboxPath = target
		return target, nil
	}

	if _, err := os.Stat(target); err == nil {
		if verifyErr := utils.VerifyChecksum(target, config.DefaultBusyboxSHA256); verifyErr == nil {
			if err := os.Chmod(target, 0o755); err != nil {
				return "", fmt.Errorf("microvm executor: chmod busybox: %w", err)
			}
			e.busyboxPath = target
			return target, nil
		} else {
			validationErr := validateBusyboxBinary(target)
			if validationErr == nil {
				if err := os.Chmod(target, 0o755); err != nil {
					return "", fmt.Errorf("microvm executor: chmod busybox: %w", err)
				}
				e.busyboxPath = target
				return target, nil
			}

			logging.Warn("microvm executor: cached busybox invalid; removing", "path", target, "checksum_error", verifyErr, "validation_error", validationErr)
			if removeErr := os.Remove(target); removeErr != nil {
				return "", fmt.Errorf("microvm executor: remove invalid busybox: %w", removeErr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("microvm executor: stat busybox: %w", err)
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	logging.Info("microvm executor: downloading support busybox", "url", config.DefaultBusyboxURL)
	tmpPath, err := utils.DownloadToTempFile(config.DefaultBusyboxURL, false)
	if err != nil {
		return "", fmt.Errorf("microvm executor: download busybox: %w (install busybox-static and ensure busybox is available locally for offline use)", err)
	}
	defer os.Remove(tmpPath)

	if err := utils.VerifyChecksum(tmpPath, config.DefaultBusyboxSHA256); err != nil {
		return "", fmt.Errorf("microvm executor: verify busybox: %w", err)
	}

	if err := copyFile(tmpPath, target, 0o755); err != nil {
		return "", fmt.Errorf("microvm executor: install busybox: %w", err)
	}

	e.busyboxPath = target
	return target, nil
}

func locateLocalBusybox() (string, error) {
	candidates := []string{}
	if envPath := strings.TrimSpace(os.Getenv("FLEDGE_BUSYBOX_PATH")); envPath != "" {
		candidates = append(candidates, envPath)
	}
	candidates = append(candidates,
		"/usr/bin/busybox",
		"/bin/busybox",
	)
	if path, err := exec.LookPath("busybox"); err == nil {
		candidates = append(candidates, path)
	}

	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			logging.Warn("microvm executor: host busybox missing execute bit", "path", candidate)
			continue
		}
		if err := validateBusyboxBinary(candidate); err != nil {
			logging.Warn("microvm executor: incompatible host busybox", "path", candidate, "error", err)
			continue
		}
		return candidate, nil
	}

	return "", nil
}

func validateBusyboxBinary(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("open ELF: %w", err)
	}
	defer f.Close()

	if f.FileHeader.Class != elf.ELFCLASS64 {
		return fmt.Errorf("expected 64-bit ELF, got %s", f.FileHeader.Class)
	}
	if f.FileHeader.Machine != elf.EM_X86_64 {
		return fmt.Errorf("expected x86_64 BusyBox binary, got %s", f.FileHeader.Machine)
	}
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			return fmt.Errorf("busybox binary is dynamically linked")
		}
	}
	return nil
}

func ensureSymlink(path, target string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if current, err := os.Readlink(path); err == nil && current == target {
				return nil
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return os.Symlink(target, path)
}

func attachLoop(imagePath string) (string, error) {
	cmd := exec.Command("losetup", "--find", "--show", imagePath)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("microvm executor: losetup: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func detachLoop(device string) {
	if device == "" {
		return
	}
	cmd := exec.Command("losetup", "-d", device)
	if output, err := cmd.CombinedOutput(); err != nil {
		logging.Warn("microvm executor: detach loop", "device", device, "error", err, "output", string(output))
	}
}

func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		_ = os.RemoveAll(dst)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}

	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode()|0o755); err != nil {
			return err
		}
		if err := clearDir(dst); err != nil {
			return err
		}

		tarCmd := exec.Command("tar", "-C", src, "-cf", "-", ".")
		untarCmd := exec.Command("tar", "-C", dst, "-xf", "-")

		pipe, err := tarCmd.StdoutPipe()
		if err != nil {
			return err
		}
		untarCmd.Stdin = pipe

		var stderr bytes.Buffer
		tarCmd.Stderr = &stderr
		untarCmd.Stderr = &stderr

		if err := untarCmd.Start(); err != nil {
			return err
		}
		if err := tarCmd.Start(); err != nil {
			untarCmd.Wait()
			return err
		}
		if err := tarCmd.Wait(); err != nil {
			untarCmd.Wait()
			return fmt.Errorf("tar copy: %w: %s", err, stderr.String())
		}
		if err := untarCmd.Wait(); err != nil {
			return fmt.Errorf("tar extract: %w: %s", err, stderr.String())
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst, info.Mode())
}

func clearDir(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.MkdirAll(path, 0o755)
		}
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	tmpPath := dst + ".tmp"
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	dstFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		return err
	}
	if err := dstFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	return nil
}

func replaceDirContents(dst, src string) error {
	dstEntries, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	for _, entry := range dstEntries {
		if err := os.RemoveAll(filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}

	srcEntries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range srcEntries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if err := copyTree(s, d); err != nil {
			return err
		}
	}
	return nil
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat != nil {
			size += stat.Blocks * 512
			return nil
		}
		size += info.Size()
		return nil
	})
	return size, err
}

func buildInitScript(process executor.ProcessInfo) string {
	var buf strings.Builder
	buf.WriteString("#!/.fledge/bin/busybox sh\n")
	buf.WriteString("set -eu\n")
	buf.WriteString("PATH=/.fledge/bin:$PATH\n")
	buf.WriteString("export PATH\n")
	buf.WriteString("export DEBIAN_FRONTEND=${DEBIAN_FRONTEND:-noninteractive}\n")
	buf.WriteString("log_console() {\n")
	buf.WriteString("\t/.fledge/bin/busybox printf '%s\\n' \"$*\" > /dev/console\n")
	buf.WriteString("}\n")
	buf.WriteString("mkdir -p /.fledge\n")
	buf.WriteString("mount -t proc proc /proc 2>/dev/null || true\n")
	buf.WriteString("mount -t sysfs sysfs /sys 2>/dev/null || true\n")
	buf.WriteString("mount -t tmpfs tmpfs /run 2>/dev/null || true\n")
	buf.WriteString("/.fledge/bin/busybox ip link set lo up 2>/dev/null || true\n")
	buf.WriteString("interfaces=\"\"\n")
	buf.WriteString("if [ -d /sys/class/net ]; then\n")
	buf.WriteString("\tinterfaces=$(/.fledge/bin/busybox ls /sys/class/net 2>/dev/null | /.fledge/bin/busybox tr '\n' ' ')\n")
	buf.WriteString("fi\n")
	buf.WriteString("if [ -z \"$interfaces\" ]; then\n")
	buf.WriteString("\tinterfaces=\"eth0 ens3 enp0s1 tap0\"\n")
	buf.WriteString("fi\n")
	buf.WriteString("log_console \"microvm init: candidate interfaces: $interfaces\"\n")
	buf.WriteString("if command -v udhcpc >/dev/null 2>&1; then\n")
	buf.WriteString("\tsuccess=0\n")
	buf.WriteString("\tfor attempt in 1 2 3 4 5; do\n")
	buf.WriteString("\t\tfor iface in $interfaces; do\n")
	buf.WriteString("\t\t\t[ \"$iface\" = \"lo\" ] && continue\n")
	buf.WriteString("\t\t\tif /.fledge/bin/busybox ip link show \"$iface\" >/dev/null 2>&1; then\n")
	buf.WriteString("\t\t\t\tlog_console \"microvm init: dhcp attempt $attempt on $iface\"\n")
	buf.WriteString("\t\t\t\t/.fledge/bin/busybox ip link set \"$iface\" up >/dev/null 2>&1 || true\n")
	buf.WriteString("\t\t\t\tif /.fledge/bin/busybox udhcpc -i \"$iface\" -n -v -t 3 -T 5 -s /.fledge/bin/udhcpc-script >>/dev/console 2>&1; then\n")
	buf.WriteString("\t\t\t\t\tsuccess=1\n")
	buf.WriteString("\t\t\t\t\tlog_console \"microvm init: dhcp success on $iface\"\n")
	buf.WriteString("\t\t\t\t\tbreak\n")
	buf.WriteString("\t\t\t\telse\n")
	buf.WriteString("\t\t\t\t\tcode=$?\n")
	buf.WriteString("\t\t\t\t\tlog_console \"microvm init: dhcp attempt $attempt on $iface failed with status $code\"\n")
	buf.WriteString("\t\t\t\tfi\n")
	buf.WriteString("\t\t\tfi\n")
	buf.WriteString("\t\t\tdone\n")
	buf.WriteString("\t\tif [ \"$success\" -eq 1 ]; then\n")
	buf.WriteString("\t\t\tbreak\n")
	buf.WriteString("\t\tfi\n")
	buf.WriteString("\t\t/.fledge/bin/busybox sleep 1\n")
	buf.WriteString("\t\tdone\n")
	buf.WriteString("\tif [ \"$success\" -ne 1 ]; then\n")
	buf.WriteString("\t\tlog_console \"microvm init: DHCP failed after retries\"\n")
	buf.WriteString("\t\techo \"microvm init: DHCP failed after retries\" >&2\n")
	buf.WriteString("\tfi\n")
	buf.WriteString("else\n")
	buf.WriteString("\techo \"microvm init: udhcpc unavailable; skipping DHCP\" >&2\n")
	buf.WriteString("fi\n")
	buf.WriteString("log_console \"microvm init: ip addr show\"\n")
	buf.WriteString("/.fledge/bin/busybox ip addr show > /dev/console\n")
	buf.WriteString("log_console \"microvm init: ip route show\"\n")
	buf.WriteString("/.fledge/bin/busybox ip route show >/dev/console 2>&1 || true\n")
	buf.WriteString("if [ -f /etc/resolv.conf ]; then\n")
	buf.WriteString("\tlog_console \"microvm init: /etc/resolv.conf\"\n")
	buf.WriteString("\t/.fledge/bin/busybox cat /etc/resolv.conf > /dev/console\n")
	buf.WriteString("fi\n")
	buf.WriteString("exec > /.fledge/stdout\n")
	buf.WriteString("exec 2> /.fledge/stderr\n")
	buf.WriteString("export HOME=${HOME:-/root}\n")

	for _, env := range process.Meta.Env {
		key, val, found := strings.Cut(env, "=")
		if !found {
			continue
		}
		buf.WriteString("export ")
		buf.WriteString(key)
		buf.WriteString("=")
		buf.WriteString(shellQuote(val))
		buf.WriteString("\n")
	}

	if cwd := strings.TrimSpace(process.Meta.Cwd); cwd != "" {
		buf.WriteString("mkdir -p ")
		buf.WriteString(shellQuote(cwd))
		buf.WriteString("\ncd ")
		buf.WriteString(shellQuote(cwd))
		buf.WriteString("\n")
	}

	buf.WriteString("set +e\n")
	buf.WriteString("set --")
	for _, arg := range process.Meta.Args {
		buf.WriteString(" ")
		buf.WriteString(shellQuote(arg))
	}
	buf.WriteString("\n")
	buf.WriteString("if [ \"$#\" -ge 1 ]; then\n")
	buf.WriteString("case \"$1\" in\n")
	buf.WriteString("/bin/sh|sh)\n")
	buf.WriteString("if [ ! -x \"$1\" ]; then\n")
	buf.WriteString("set -- /.fledge/bin/busybox sh \"${@:2}\"\n")
	buf.WriteString("fi\n")
	buf.WriteString(";;\n")
	buf.WriteString("esac\n")
	buf.WriteString("fi\n")
	buf.WriteString("log_console \"microvm init: executing command: $*\"\n")
	buf.WriteString("\"$@\"\n")
	buf.WriteString("status=$?\n")
	buf.WriteString("log_console \"microvm init: command exited with status $status\"\n")
	buf.WriteString("set -e\n")
	buf.WriteString("printf '%s\n' $status > /.fledge/exit_code\n")
	buf.WriteString("sync\n")
	buf.WriteString("poweroff -f >/dev/null 2>&1 || halt -f >/dev/null 2>&1 || reboot -f >/dev/null 2>&1 || echo o > /proc/sysrq-trigger\n")
	buf.WriteString("sleep 60\n")
	buf.WriteString("exit $status\n")
	return buf.String()
}

func shellQuote(val string) string {
	if val == "" {
		return "''"
	}
	if strings.ContainsAny(val, "\n\000") {
		val = strings.ReplaceAll(val, "\n", " ")
	}
	if !strings.ContainsAny(val, " \t\"'\\$`!#&()*;<>?[]{}|~") {
		return val
	}
	return "'" + strings.ReplaceAll(val, "'", "'\"'\"'") + "'"
}

func (e *Executor) allocateVMName(id string) string {
	e.tempMu.Lock()
	defer e.tempMu.Unlock()
	e.nextVMID++
	if id == "" {
		return fmt.Sprintf("fledge-run-%d", e.nextVMID)
	}
	return fmt.Sprintf("%s-%d", sanitizeName(id), e.nextVMID)
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "run"
	}
	var buf strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			buf.WriteRune(r)
		} else if r == ' ' || r == '_' || r == '/' {
			buf.WriteRune('-')
		}
	}
	if buf.Len() == 0 {
		return "run"
	}
	return buf.String()
}

func buildUDHCPCScript() string {
	script := `
#!/.fledge/bin/busybox sh
set -eu

case "$1" in
deconfig)
	/.fledge/bin/busybox ip addr flush dev "$interface" >/dev/null 2>&1 || true
	/.fledge/bin/busybox ip link set "$interface" down >/dev/null 2>&1 || true
	;;
bound|renew)
	/.fledge/bin/busybox ip addr flush dev "$interface" >/dev/null 2>&1 || true
	if [ -n "${subnet:-}" ]; then
		/.fledge/bin/busybox ifconfig "$interface" "$ip" netmask "$subnet" up
	else
		/.fledge/bin/busybox ifconfig "$interface" "$ip" up
	fi
	/.fledge/bin/busybox ip route flush dev "$interface" >/dev/null 2>&1 || true
	if [ -n "${router:-}" ]; then
		/.fledge/bin/busybox ip route add default via "$router" dev "$interface" >/dev/null 2>&1 || true
	fi
	> /.fledge/resolv.conf
	if [ -n "${dns:-}" ]; then
		for server in $dns; do
			printf "nameserver %s\n" "$server" >> /.fledge/resolv.conf
		done
	fi
	mkdir -p /etc
	if [ -s /.fledge/resolv.conf ]; then
		cp /.fledge/resolv.conf /etc/resolv.conf
	fi
	;;
*)
	;;
esac

exit 0
`
	return strings.TrimPrefix(script, "\n")
}
