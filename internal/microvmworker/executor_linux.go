//go:build linux

package microvmworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/mount"
	"github.com/moby/buildkit/executor"
	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	ch "github.com/volantvm/fledge/internal/launcher"
	"github.com/volantvm/fledge/internal/logging"
)

// Executor runs BuildKit exec steps inside Cloud Hypervisor microVMs.
type Executor struct {
	worker     *Worker
	workspace  string
	tempMu     sync.Mutex
	nextVMID   int
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

	return &Executor{
		worker:     w,
		workspace:  workspace,
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
	inst, err := e.worker.BootVM(ctx, vmName, ch.LaunchSpec{
		Name:         vmName,
		CPUCores:     2,
		MemoryMB:     1536,
		KernelArgs:   e.baseKernel,
		DiskPath:     imagePath,
		ReadOnlyRoot: false,
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
	size, err := dirSize(rootDir)
	if err != nil {
		return "", fmt.Errorf("microvm executor: size rootfs: %w", err)
	}

	extra := int64(256 << 20) // 256MB buffer
	if size < 1<<30 {
		extra = int64(128 << 20)
	}
	if size < 512<<20 {
		extra = int64(64 << 20)
	}
	if size < 128<<20 {
		extra = int64(32 << 20)
	}

	total := size + extra
	if total < 64<<20 {
		total = 64 << 20
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

	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", imagePath)
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
		return e.writeInitFiles(mountPoint, process)
	})
}

func (e *Executor) collectResults(ctx context.Context, imagePath, rootDir string, process executor.ProcessInfo) ([]byte, []byte, int, error) {
	var stdoutBuf, stderrBuf []byte
	exitCode := -1

	err := e.withDiskMount(ctx, imagePath, func(mountPoint string) error {
		ctrlDir := filepath.Join(mountPoint, ".fledge")
		stdoutBuf, _ = os.ReadFile(filepath.Join(ctrlDir, "stdout"))
		stderrBuf, _ = os.ReadFile(filepath.Join(ctrlDir, "stderr"))
		if data, err := os.ReadFile(filepath.Join(ctrlDir, "exit_code")); err == nil {
			if v, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
				exitCode = v
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

func (e *Executor) writeInitFiles(mountPoint string, process executor.ProcessInfo) error {
	controlDir := filepath.Join(mountPoint, ".fledge")
	if err := os.MkdirAll(controlDir, 0o755); err != nil {
		return err
	}

	initPath := filepath.Join(controlDir, "init")
	script := buildInitScript(process)
	if err := os.WriteFile(initPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write init script: %w", err)
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
		size += info.Size()
		return nil
	})
	return size, err
}

func buildInitScript(process executor.ProcessInfo) string {
	var buf strings.Builder
	buf.WriteString("#!/bin/sh\n")
	buf.WriteString("set -eu\n")
	buf.WriteString("mkdir -p /.fledge\n")
	buf.WriteString("mount -t proc proc /proc 2>/dev/null || true\n")
	buf.WriteString("mount -t sysfs sysfs /sys 2>/dev/null || true\n")
	buf.WriteString("mount -t tmpfs tmpfs /run 2>/dev/null || true\n")
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
	buf.WriteString("\"$@\"\n")
	buf.WriteString("status=$?\n")
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
