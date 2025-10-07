//go:build linux

package microvmworker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes/docker"
	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/moby/buildkit/cache"
	bkmetadata "github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor/resources"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/winlayers"
	"github.com/moby/buildkit/version"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	wlabel "github.com/moby/buildkit/worker/label"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"

	ch "github.com/volantvm/fledge/internal/launcher"
)

// Worker is a skeleton for a BuildKit worker that executes steps inside
// Cloud Hypervisor microVMs.
type Worker struct {
	Launcher      *ch.Launcher
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
func (w *Worker) BootVM(ctx context.Context, name string, spec ch.LaunchSpec) (ch.Instance, error) {
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

// NewBuildkitWorker constructs a BuildKit worker backed by the microVM executor.
func (w *Worker) NewBuildkitWorker(ctx context.Context, root string, hosts docker.RegistryHosts) (worker.Worker, error) {
	if w == nil {
		return nil, fmt.Errorf("microvmworker: worker not configured")
	}
	if root == "" {
		root = filepath.Join(w.RuntimeDir, "buildkit")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("microvmworker: ensure state dir: %w", err)
	}

	exe, err := NewExecutor(w)
	if err != nil {
		return nil, err
	}

	snapshotRoot := filepath.Join(root, "snapshots")
	if err := os.MkdirAll(snapshotRoot, 0o700); err != nil {
		return nil, fmt.Errorf("microvmworker: ensure snapshot dir: %w", err)
	}

	sn, err := native.NewSnapshotter(snapshotRoot)
	if err != nil {
		return nil, fmt.Errorf("microvmworker: create snapshotter: %w", err)
	}

	contentStore, err := local.NewStore(filepath.Join(root, "content"))
	if err != nil {
		return nil, fmt.Errorf("microvmworker: create content store: %w", err)
	}

	metadataDB, err := bolt.Open(filepath.Join(root, "containerdmeta.db"), 0o644, nil)
	if err != nil {
		return nil, fmt.Errorf("microvmworker: open metadata db: %w", err)
	}

	mdb := ctdmetadata.NewDB(metadataDB, contentStore, map[string]ctdsnapshot.Snapshotter{
		"native": sn,
	})
	if err := mdb.Init(ctx); err != nil {
		return nil, fmt.Errorf("microvmworker: init metadata db: %w", err)
	}

	cs := containerdsnapshot.NewContentStore(mdb.ContentStore(), "buildkit")

	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), "buildkit")
	snap := containerdsnapshot.NewSnapshotter("native", mdb.Snapshotter("native"), "buildkit", nil)
	if err := cache.MigrateV2(ctx, filepath.Join(root, "metadata.db"), filepath.Join(root, "metadata_v2.db"), cs, snap, lm); err != nil {
		return nil, fmt.Errorf("microvmworker: migrate metadata: %w", err)
	}

	md, err := bkmetadata.NewStore(filepath.Join(root, "metadata_v2.db"))
	if err != nil {
		return nil, fmt.Errorf("microvmworker: open cache metadata: %w", err)
	}

	rm, err := resources.NewMonitor()
	if err != nil {
		return nil, fmt.Errorf("microvmworker: resource monitor: %w", err)
	}

	id, err := base.ID(root)
	if err != nil {
		return nil, fmt.Errorf("microvmworker: derive worker id: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	labels := map[string]string{
		wlabel.Executor:    "microvm",
		wlabel.Snapshotter: "native",
		wlabel.Hostname:    hostname,
	}

	opt := base.WorkerOpt{
		ID:              id,
		Labels:          labels,
		Platforms:       []ocispecs.Platform{platforms.Normalize(platforms.DefaultSpec())},
		BuildkitVersion: client.BuildkitVersion{Package: version.Package, Version: version.Version, Revision: version.Revision},
		Executor:        exe,
		Snapshotter:     snap,
		ContentStore:    cs,
		Applier:         winlayers.NewFileSystemApplierWithWindows(cs, apply.NewFileSystemApplier(cs)),
		Differ:          winlayers.NewWalkingDiffWithWindows(cs, walking.NewWalkingDiff(cs)),
		RegistryHosts:   hosts,
		IdentityMapping: nil,
		LeaseManager:    lm,
		GarbageCollect:  mdb.GarbageCollect,
		MetadataStore:   md,
		MountPoolRoot:   filepath.Join(root, "cachemounts"),
		ResourceMonitor: rm,
	}

	if err := os.MkdirAll(opt.MountPoolRoot, 0o755); err != nil {
		return nil, fmt.Errorf("microvmworker: ensure mount pool: %w", err)
	}

	wk, err := base.NewWorker(ctx, opt)
	if err != nil {
		return nil, err
	}

	return wk, nil
}
