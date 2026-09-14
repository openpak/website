// Command website serves openpak.org: public landing, account portal,
// moderation pages and the versioned mobile API.
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openpak/website/internal/admin"
	"github.com/openpak/website/internal/api"
	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/config"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/metrics"
	"github.com/openpak/website/internal/netprofile"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/push"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

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
		reqs, err := store.CatalogRequests()
		if err != nil {
			return err
		}
		// Upsert, never truncate: statuses and notes refresh from the catalog,
		// but votes (user data) are left untouched.
		if err := db.SeedRequests(ctx, reqs); err != nil {
			log.Warn("seed requests", "err", err)
		}
		if cfg.CatalogURL != "" {
			go refreshCatalog(ctx, db, cfg.CatalogURL, cfg.CatalogRefresh, log)
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
	savesCli := saves.New(os.Getenv("SAVES_URL"), os.Getenv("SAVES_INTERNAL_KEY"))
	if savesCli == nil {
		log.Info("saves: SAVES_URL not set; the saves page says so and the API answers 503")
	}
	pageSrv.Saves = savesCli

	chatCli := chat.New(os.Getenv("CHAT_URL"), os.Getenv("CHAT_INTERNAL_KEY"))
	if chatCli == nil {
		log.Info("chat: CHAT_URL not set; the chat page says so and the API answers 503")
	}
	pageSrv.Chat = chatCli

	apiSrv := api.New(coreHTTP, coreGRPC, coreGRPC, coreGRPC, coreGRPC, db, sessions, mediaSvc, log)
	apiSrv.Federation = coreGRPC
	apiSrv.Saves = savesCli
	apiSrv.Chat = chatCli

	// The network profile (PRD network-profile-service-prd): generated from
	// the live Traefik routing, so adding a title is a server deploy, not six
	// client releases. The file is mounted read-only; without it there is
	// nothing to generate from and /api/v1/network/* answers 503 while the
	// rest of the site is unaffected.
	if cfg.NetProfileTraefikFile != "" {
		np, err := netprofile.New(netprofile.Options{
			TraefikFile: cfg.NetProfileTraefikFile,
			PlatformMap: cfg.NetProfilePlatformMap,
			CA:          apiSrv.Adapter.CA,
			Log:         log,
		})
		if err != nil {
			return err
		}
		apiSrv.Profiles = np
		log.Info("network profile enabled", "traefik", cfg.NetProfileTraefikFile)
	} else {
		log.Info("network profile: NETPROFILE_TRAEFIK_FILE not set; /api/v1/network/* answers 503")
	}

	// The v2 bundle registry (PRD universal-tls-forwarding/01): signed,
	// authenticated, build-time distribution. Absent REGISTRY_* variables is
	// a supported configuration: /api/v2/network/* answers 503 while the
	// rest of the site is unaffected, exactly like the v1 profile surface
	// before it.
	registrySvc, registryCI, closeRegistry := api.ConfigureV2Registry(log)
	defer closeRegistry()
	if registrySvc != nil {
		apiSrv.Registry = registrySvc
		apiSrv.CIVerifier = registryCI
		log.Info("network registry enabled", "policy", registrySvc.PolicyID)
	} else {
		log.Info("network registry: REGISTRY_* not set; /api/v2/network/* answers 503")
	}

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
	adminSrv.Federation = coreGRPC

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

// refreshCatalog keeps the title list in step with the live catalog: fetch, and when the body
// changed since last time, reseed (an upsert; votes are untouched). A failed fetch keeps the
// last good state, so the page never goes empty because GitHub blinked.
func refreshCatalog(ctx context.Context, db store.Store, url string, every time.Duration, log *slog.Logger) {
	client := &http.Client{Timeout: 20 * time.Second}
	var last [32]byte
	fetch := func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Warn("catalog refresh", "err", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Warn("catalog refresh", "status", resp.Status)
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			log.Warn("catalog refresh", "err", err)
			return
		}
		sum := sha256.Sum256(body)
		if sum == last {
			return
		}
		reqs, err := store.ParseCatalog(body)
		if err != nil {
			log.Warn("catalog refresh: bad catalog.json, keeping the previous one", "err", err)
			return
		}
		if err := db.SeedRequests(ctx, reqs); err != nil {
			log.Warn("catalog refresh: seed", "err", err)
			return
		}
		last = sum
		log.Info("catalog refreshed", "entries", len(reqs))
	}
	fetch()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fetch()
		}
	}
}
