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
	"github.com/openpak/website/internal/news"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/push"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

func main() {
	// WEBSITE_LOG_LEVEL=debug also logs every request; anything else is info.
	level := slog.LevelInfo
	if os.Getenv("WEBSITE_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
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
		// Upsert: new rows and feature statuses come from the catalog, title statuses
		// stay the database's, and votes (user data) are left untouched.
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
		// Every stored avatar also goes to the core's Media service (CR-2), so
		// adapters can serve it to consoles. Best effort: a failure is logged.
		mediaSvc.Publisher, mediaSvc.Log = coreGRPC, log
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
	apiSrv.SaveTitle = pages.SaveTitle
	apiSrv.Chat = chatCli

	// The network profile (PRD prds/network-profile-service-prd.md): generated from
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

	// The v2 bundle registry (/api/v2/network/*) is a separate process,
	// cmd/openpak-registry: its signing key and database never load here.

	// Push. Absent credentials is a supported configuration: the site runs, the
	// app can still register a token, and nothing is delivered until a service
	// account exists.
	pushOn := false
	if fcm, err := push.NewFCM(cfg.FCMCredentialsFile); err != nil {
		return err
	} else if fcm == nil {
		log.Info("push: no FCM credentials configured; notifications are disabled")
	} else {
		notifier := push.NewNotifier(coreGRPC, coreGRPC, coreGRPC, db, fcm, log)
		go notifier.Run(ctx)
		pushOn = true
		log.Info("push: notifier started")
	}
	adminSrv := admin.New(tmpl, coreGRPC, db, sessions, cfg.AdminAccountIDs, log)
	adminSrv.Federation = coreGRPC
	adminSrv.Moderation = coreGRPC
	// The monitor's sources. Each is optional and reads as "not configured"
	// when absent; a typed nil must not reach an interface field, or it would
	// read as configured and broken.
	adminSrv.CoreHTTP = coreHTTP
	if mediaSvc != nil {
		adminSrv.Media = mediaSvc
	}
	adminSrv.Players = coreGRPC
	adminSrv.Saves = savesCli
	adminSrv.Chat = chatCli
	adminSrv.PushOn = pushOn
	adminSrv.StatusAPI = envOr("STATUS_API_URL", "https://status.openpak.org/api/status")
	adminSrv.Mods = admin.NewModsAPI(os.Getenv("MODS_INTERNAL_URL"), os.Getenv("MODS_INTERNAL_KEY"))
	adminSrv.Registry = admin.NewRegistryAPI(os.Getenv("REGISTRY_INTERNAL_URL"), os.Getenv("REGISTRY_ADMIN_KEY"))
	adminSrv.Ops = admin.NewOpsAPI(os.Getenv("WEBSITE_OPSD_HOSTS"), os.Getenv("WEBSITE_OPSD_KEY"))

	// News: the public feed for the landing page, and the operator gRPC for
	// /admin/news. Either can be absent; the landing page then has no news
	// section and the admin page says the service is not configured.
	pageSrv.News = news.NewFeed(os.Getenv("NEWS_URL"))
	apiSrv.News = pageSrv.News // GET /api/v1/news, for the app
	if addr, key := os.Getenv("NEWS_GRPC_ADDR"), os.Getenv("NEWS_OPERATOR_KEY"); addr != "" && key != "" {
		newsCli, conn, err := news.DialOperator(addr, key)
		if err != nil {
			return err
		}
		defer conn.Close()
		adminSrv.News = newsCli
	} else {
		log.Info("news: NEWS_GRPC_ADDR/NEWS_OPERATOR_KEY not set; /admin/news says so")
	}

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
		Addr: cfg.HTTPAddr,
		Handler: metrics.Middleware(logRequests(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mux.ServeHTTP(w, r.WithContext(core.WithLang(r.Context(), i18n.FromRequest(r))))
		}))),
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

// envOr reads an optional URL with a default; the literal "off" turns it off.
func envOr(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		v = def
	}
	if v == "off" {
		return ""
	}
	return v
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
