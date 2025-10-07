//go:build linux

package embedded

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/moby/buildkit/cache/remotecache"
	inlineremotecache "github.com/moby/buildkit/cache/remotecache/inline"
	localremotecache "github.com/moby/buildkit/cache/remotecache/local"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/frontend/dockerfile/builder"
	fgateway "github.com/moby/buildkit/frontend/gateway"
	"github.com/moby/buildkit/frontend/gateway/forwarder"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/util/resolver"
	"github.com/moby/buildkit/worker"
	"github.com/volantvm/fledge/internal/microvmworker"
	"go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const (
	bufConnSize = 32 << 20
)

// BuildDockerfileToRootfs executes a Dockerfile build using an embedded BuildKit
// controller backed by the microVM worker. The build output is exported to the
// provided destination directory.
func BuildDockerfileToRootfs(ctx context.Context, dockerfile, contextDir, target string, buildArgs map[string]string, destDir string) error {
	stateDir, err := ensureStateDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("embedded buildkit: create dest dir: %w", err)
	}

	client, cleanup, err := newEmbeddedClient(ctx, stateDir)
	if err != nil {
		return err
	}
	defer cleanup()

	dfDir := filepath.Dir(dockerfile)
	dfBase := filepath.Base(dockerfile)

	frontendAttrs := map[string]string{
		"filename": dfBase,
	}
	if target != "" {
		frontendAttrs["target"] = target
	}
	for k, v := range buildArgs {
		frontendAttrs["build-arg:"+k] = v
	}

	solveOpt := bkclient.SolveOpt{
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
		LocalDirs: map[string]string{
			"context":    contextDir,
			"dockerfile": dfDir,
		},
		Exports: []bkclient.ExportEntry{
			{
				Type:      bkclient.ExporterLocal,
				OutputDir: destDir,
			},
		},
	}

	statusCh := make(chan *bkclient.SolveStatus, 16)
	var progressWG sync.WaitGroup
	progressWG.Add(1)
	go func() {
		defer progressWG.Done()
		for st := range statusCh {
			for _, v := range st.Vertexes {
				if v == nil {
					continue
				}
				switch {
				case v.Completed != nil:
					log.Printf("embedded buildkit: step complete: %s", v.Name)
				case v.Error != "":
					log.Printf("embedded buildkit: step error: %s: %s", v.Name, v.Error)
				case v.Started != nil:
					log.Printf("embedded buildkit: step started: %s", v.Name)
				}
			}
			for _, s := range st.Statuses {
				if s == nil {
					continue
				}
				name := s.Name
				if name == "" {
					name = s.ID
				}
				if name == "" {
					continue
				}
				if s.Total > 0 {
					log.Printf("embedded buildkit: status %s %d/%d", name, s.Current, s.Total)
					continue
				}
				log.Printf("embedded buildkit: status %s", name)
			}
		}
	}()

	_, err = client.Solve(ctx, nil, solveOpt, statusCh)
	progressWG.Wait()
	if err != nil {
		return fmt.Errorf("embedded buildkit: solve failed: %w", err)
	}
	return nil
}

func ensureStateDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("FLEDGE_BUILDKIT_STATE_DIR")); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", fmt.Errorf("embedded buildkit: resolve state dir: %w", err)
		}
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return "", fmt.Errorf("embedded buildkit: create state dir: %w", err)
		}
		return abs, nil
	}

	if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
		path := filepath.Join(cacheDir, "fledge", "buildkit")
		if err := os.MkdirAll(path, 0o700); err != nil {
			return "", fmt.Errorf("embedded buildkit: create cache dir: %w", err)
		}
		return path, nil
	}

	path := filepath.Join(os.TempDir(), "fledge-buildkit")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("embedded buildkit: create temp dir: %w", err)
	}
	return path, nil
}

func newEmbeddedClient(ctx context.Context, stateDir string) (_ *bkclient.Client, cleanup func(), err error) {
	sm, err := session.NewManager()
	if err != nil {
		return nil, nil, fmt.Errorf("embedded buildkit: session manager: %w", err)
	}

	runtimeDir := filepath.Join(stateDir, "runtime")
	mw, err := microvmworker.NewFromEnv(runtimeDir)
	if err != nil {
		return nil, nil, err
	}

	workerRoot := filepath.Join(stateDir, "worker")
	registryHosts := resolver.NewRegistryConfig(nil)
	wk, err := mw.NewBuildkitWorker(ctx, workerRoot, registryHosts)
	if err != nil {
		return nil, nil, err
	}

	wc := &worker.Controller{}
	if err := wc.Add(wk); err != nil {
		wk.Close()
		return nil, nil, err
	}

	defer func() {
		if err != nil {
			wc.Close()
		}
	}()

	cachePath := filepath.Join(stateDir, "cache.db")
	cacheStorage, err := bboltcachestorage.NewStore(cachePath)
	if err != nil {
		return nil, nil, fmt.Errorf("embedded buildkit: cache store: %w", err)
	}

	defer func() {
		if err != nil {
			cacheStorage.Close()
		}
	}()

	historyPath := filepath.Join(stateDir, "history.db")
	historyDB, err := bbolt.Open(historyPath, 0o600, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("embedded buildkit: history db: %w", err)
	}

	defer func() {
		if err != nil {
			historyDB.Close()
		}
	}()

	defaultWorker, err := wc.GetDefault()
	if err != nil {
		return nil, nil, err
	}

	contentStore := defaultWorker.ContentStore()
	if contentStore == nil {
		return nil, nil, fmt.Errorf("embedded buildkit: worker content store unavailable")
	}

	leaseManager := defaultWorker.LeaseManager()
	if leaseManager == nil {
		return nil, nil, fmt.Errorf("embedded buildkit: worker lease manager unavailable")
	}

	frontends := map[string]frontend.Frontend{
		"dockerfile.v0": forwarder.NewGatewayForwarder(wc.Infos(), builder.Build),
		"gateway.v0":    fgateway.NewGatewayFrontend(wc.Infos()),
	}

	cacheMgr := solver.NewCacheManager(context.TODO(), identity.NewID(), cacheStorage, worker.NewCacheResultStorage(wc))

	cacheExporters := map[string]remotecache.ResolveCacheExporterFunc{
		"local":  localremotecache.ResolveCacheExporterFunc(sm),
		"inline": inlineremotecache.ResolveCacheExporterFunc(),
	}

	cacheImporters := map[string]remotecache.ResolveCacheImporterFunc{
		"local": localremotecache.ResolveCacheImporterFunc(sm),
	}

	controller, ctrlErr := control.NewController(control.Opt{
		SessionManager:            sm,
		WorkerController:          wc,
		Frontends:                 frontends,
		CacheManager:              cacheMgr,
		ResolveCacheExporterFuncs: cacheExporters,
		ResolveCacheImporterFuncs: cacheImporters,
		Entitlements:              nil,
		HistoryDB:                 historyDB,
		CacheStore:                cacheStorage,
		LeaseManager:              leaseManager,
		ContentStore:              contentStore,
		HistoryConfig:             nil,
	})
	if ctrlErr != nil {
		return nil, nil, ctrlErr
	}

	listener := bufconn.Listen(bufConnSize)
	server := grpc.NewServer()
	controller.Register(server)

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			serverErr <- err
		}
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}

	client, err := bkclient.New(ctx, "", bkclient.WithContextDialer(dialer))
	if err != nil {
		server.Stop()
		listener.Close()
		controller.Close()
		return nil, nil, err
	}

	cleanup = func() {
		if client != nil {
			client.Close()
		}
		server.Stop()
		listener.Close()
		if err := controller.Close(); err != nil {
			log.Printf("embedded buildkit: controller close error: %v", err)
		}
		select {
		case err := <-serverErr:
			if err != nil {
				log.Printf("embedded buildkit: server error: %v", err)
			}
		default:
		}
	}

	return client, cleanup, nil
}
