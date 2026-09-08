// Command website serves openpak.org: public landing, account portal,
// moderation pages and the versioned mobile API.
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

	"github.com/openpak/website/internal/admin"
	"github.com/openpak/website/internal/api"
	"github.com/openpak/website/internal/config"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/metrics"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/push"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

// defaultTitles seeds the supported-titles table on first boot when
// WEBSITE_SEED_TITLES is set. Ship honest placeholders only.
var defaultTitles = []store.Title{
	{Name: "To be announced", Platform: "Switch", Status: "planned"},
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Website-owned Postgres.
	db, err := store.New(ctx, cfg.SiteDatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.SeedTitles {
		if err := db.SeedTitles(ctx, defaultTitles); err != nil {
			log.Warn("seed titles", "err", err)
		}
	}

	// Core clients: versioned HTTP + gRPC contracts only.
	coreHTTP, err := core.NewHTTP(cfg.CoreBaseURL, cfg.CoreHTTPTimeout)
	if err != nil {
		return err
	}
	coreGRPC, err := core.DialGRPC(cfg.CoreGRPCAddr, cfg.CoreInternalKey)
	if err != nil {
		return err
	}
	defer coreGRPC.Close()

	sessions := session.New(cfg.SessionCookieName, cfg.SessionSecret, cfg.Environment == "production", cfg.CoreSessionTTL)

	var mediaSvc *media.Service
	if st, err := media.NewFSStorage(cfg.MediaDir); err == nil {
		mediaSvc = media.New(st)
	} else {
		log.Warn("media storage unavailable; avatar uploads disabled", "err", err)
	}

	tmpl, err := pages.LoadTemplates()
	if err != nil {
		return err
	}

	pageSrv := pages.New(tmpl, log)
	pageSrv.Core = coreHTTP
	pageSrv.Identity = coreGRPC
	pageSrv.Social = coreGRPC
	pageSrv.Links = coreGRPC
	pageSrv.Events = coreGRPC
	pageSrv.Invites = coreGRPC
	pageSrv.DB = db
	pageSrv.Media = mediaSvc
	pageSrv.Sessions = sessions
	pageSrv.Production = cfg.Environment == "production"
	pageSrv.AdminAccountIDs = cfg.AdminAccountIDs
	pageSrv.AuthLimit = ratelimit.New(20, time.Minute)
	pageSrv.MediaLimit = ratelimit.New(5, time.Hour)

	apiSrv := api.New(coreHTTP, coreGRPC, coreGRPC, coreGRPC, coreGRPC, db, sessions, log)

	// Push. Absent credentials is a supported configuration: the site runs, the
	// app can still register a token, and nothing is delivered until a service
	// account exists.
	if fcm, err := push.NewFCM(cfg.FCMCredentialsFile); err != nil {
		return err
	} else if fcm == nil {
		log.Info("push: no FCM credentials configured; notifications are disabled")
	} else {
		notifier := push.NewNotifier(coreGRPC, coreGRPC, coreGRPC, db, fcm, log)
		go notifier.Run(ctx)
		log.Info("push: notifier started")
	}
	adminSrv := admin.New(tmpl, coreGRPC, db, sessions, cfg.AdminAccountIDs, log)

	mux := http.NewServeMux()
	pageSrv.RegisterRoutes(mux)
	apiSrv.RegisterRoutes(mux)
	adminSrv.RegisterRoutes(mux)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"website"}`))
	})

	// Minimal metrics/pprof listener on the website block's +2 port.
	metrics := metrics.New()
	metricsSrv := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metrics.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("metrics listening", "addr", cfg.MetricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           metrics.Middleware(logRequests(log)(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("website listening", "addr", cfg.HTTPAddr, "environment", cfg.Environment, "lang", i18n.Lang("en"))

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
