package server

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "strings"
    "time"

    "github.com/volantvm/fledge/internal/config"
    "github.com/volantvm/fledge/internal/logging"
)

type Options struct {
    Addr        string
    APIKey      string
    CORSOrigins []string
}

type buildRequest struct {
    ConfigPath string `json:"config_path"`
    OutputPath string `json:"output_path"`
}

type buildResponse struct {
    Output string `json:"output"`
}

// Start launches the HTTP server and blocks until the context is done or the server exits.
func Start(ctx context.Context, opts Options, buildFn func(ctx context.Context, cfg *config.Config, workDir, output string) error, initramfsFn func(ctx context.Context, cfg *config.Config, workDir, output string) error) error {
    mux := http.NewServeMux()

    wrap := func(h http.HandlerFunc) http.HandlerFunc {
        return func(w http.ResponseWriter, r *http.Request) {
            if !allowOrigin(w, r, opts.CORSOrigins) {
                http.Error(w, "CORS not allowed", http.StatusForbidden)
                return
            }
            if r.Method == http.MethodOptions {
                w.WriteHeader(http.StatusNoContent)
                return
            }
            if opts.APIKey != "" && !authOK(r, opts.APIKey) {
                http.Error(w, "unauthorized", http.StatusUnauthorized)
                return
            }
            h(w, r)
        }
    }

    mux.HandleFunc("/v1/healthz", wrap(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    }))

    mux.HandleFunc("/v1/build", wrap(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        var req buildRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            http.Error(w, "invalid json", http.StatusBadRequest)
            return
        }
        if req.ConfigPath == "" {
            http.Error(w, "config_path required", http.StatusBadRequest)
            return
        }
        cfg, err := config.Load(req.ConfigPath)
        if err != nil {
            http.Error(w, fmt.Sprintf("config error: %v", err), http.StatusBadRequest)
            return
        }
        workDir := dirOf(req.ConfigPath)
        output := req.OutputPath
        if output == "" {
            output = defaultOutput(cfg)
        }

        ctx2, cancel := context.WithTimeout(ctx, 12*time.Hour)
        defer cancel()

        switch cfg.Strategy {
        case config.StrategyOCIRootfs:
            if err := buildFn(ctx2, cfg, workDir, output); err != nil {
                http.Error(w, fmt.Sprintf("build failed: %v", err), http.StatusInternalServerError)
                return
            }
        case config.StrategyInitramfs:
            if err := initramfsFn(ctx2, cfg, workDir, output); err != nil {
                http.Error(w, fmt.Sprintf("build failed: %v", err), http.StatusInternalServerError)
                return
            }
        default:
            http.Error(w, "unsupported strategy", http.StatusBadRequest)
            return
        }

        json.NewEncoder(w).Encode(buildResponse{Output: output})
    }))

    srv := &http.Server{
        Addr:              opts.Addr,
        Handler:           mux,
        ReadHeaderTimeout: 15 * time.Second,
    }

    errCh := make(chan error, 1)
    go func() {
        logging.Info("Fledge daemon listening", "addr", opts.Addr)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            errCh <- err
        }
    }()

    select {
    case <-ctx.Done():
        ctxShutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        _ = srv.Shutdown(ctxShutdown)
        return nil
    case err := <-errCh:
        return err
    }
}

func authOK(r *http.Request, apiKey string) bool {
    if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
        return strings.TrimPrefix(h, "Bearer ") == apiKey
    }
    if h := r.Header.Get("X-API-Key"); h != "" {
        return h == apiKey
    }
    return false
}

func allowOrigin(w http.ResponseWriter, r *http.Request, origins []string) bool {
    origin := r.Header.Get("Origin")
    if origin == "" {
        return true
    }
    allowed := false
    if len(origins) == 0 {
        // default: allow localhost for dev
        if strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
            allowed = true
        }
    } else {
        for _, o := range origins {
            if o == "*" || o == origin {
                allowed = true
                break
            }
        }
    }
    if allowed {
        w.Header().Set("Access-Control-Allow-Origin", origin)
        w.Header().Set("Vary", "Origin")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
        w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
    }
    return allowed
}

func defaultOutput(cfg *config.Config) string {
    // mimic CLI auto naming
    ext := ".bin"
    switch cfg.Strategy {
    case config.StrategyOCIRootfs:
        ext = ".img"
    case config.StrategyInitramfs:
        ext = ".cpio.gz"
    }
    base := "plugin"
    if cfg.Strategy == config.StrategyOCIRootfs && cfg.Source.Image != "" {
        s := cfg.Source.Image
        if i := strings.LastIndex(s, ":"); i > 0 {
            s = s[:i]
        }
        if i := strings.LastIndex(s, "/"); i >= 0 {
            s = s[i+1:]
        }
        base = strings.ToLower(strings.ReplaceAll(s, " ", "-"))
    }
    return base + ext
}

func dirOf(p string) string {
    if p == "" {
        return "."
    }
    if !strings.HasPrefix(p, "/") {
        wd, _ := os.Getwd()
        return wd
    }
    // crude but fine for server endpoint using absolute paths
    i := strings.LastIndex(p, "/")
    if i <= 0 {
        return "/"
    }
    return p[:i]
}
