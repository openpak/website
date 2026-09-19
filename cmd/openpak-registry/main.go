// Command openpak-registry serves the v2 network distribution surface
// (/api/v2/network/*, PRD emulators/prds/universal-tls-forwarding/01) and the
// operator's GET /internal/status. It is its own process because the
// registry is its own trust domain: the online signing key, the registry
// database and the CI identity never load into the website. Traefik routes
// openpak.org/api/v2/ here; see docs/registry.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openpak/website/internal/metrics"
	"github.com/openpak/website/internal/registryhttp"
)

func main() {
	// REGISTRY_LOG_LEVEL=debug also logs every request; anything else is info.
	level := slog.LevelInfo
	if os.Getenv("REGISTRY_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// env reads a variable with a port-plan default (registry block 20140).
func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func run(log *slog.Logger) error {
	httpAddr := env("REGISTRY_HTTP_ADDR", ":20140")
	metricsAddr := env("REGISTRY_METRICS_ADDR", ":20142")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Absent REGISTRY_* variables is a supported configuration: the routes
	// answer 503, the same as the website did before the split, so the
	// container can be deployed ahead of its keys.
	svc, ci, closeRegistry := registryhttp.Configure(log)
	defer closeRegistry()
	if svc != nil {
		log.Info("network registry enabled", "policy", svc.PolicyID)
	} else {
		log.Info("network registry: REGISTRY_* not set; /api/v2/network/* answers 503")
	}
	regSrv := registryhttp.New(svc, ci, log)
	regSrv.AdminKey = os.Getenv("REGISTRY_ADMIN_KEY")
	if regSrv.AdminKey == "" {
		log.Info("REGISTRY_ADMIN_KEY not set; /internal/status answers 404")
	}

	mux := http.NewServeMux()
	regSrv.RegisterRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"registry"}`))
	})

	// Metrics/pprof on the registry block's +2 port, never through Traefik.
	metrics := metrics.NewFor("registry")
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metrics.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("metrics listening", "addr", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           metrics.Middleware(logRequests(log)(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("registry listening", "addr", httpAddr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = metricsSrv.Shutdown(shutdownCtx)
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func logRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			log.Debug("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start).String())
		})
	}
}
