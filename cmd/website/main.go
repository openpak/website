// Command website serves openpak.org: public landing, account portal,
// moderation pages and the versioned mobile API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/admin"
	"github.com/openpak/website/internal/api"
	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/config"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/erasure"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/metrics"
	"github.com/openpak/website/internal/netprofile"
	"github.com/openpak/website/internal/news"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/pctl"
	"github.com/openpak/website/internal/push"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
	"github.com/openpak/website/internal/turnstile"
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
	pageSrv.Moderation = coreGRPC
	pageSrv.Links = coreGRPC
	pageSrv.Federation = coreGRPC
	pageSrv.Operators = db
	pageSrv.Events = coreGRPC
	pageSrv.Invites = coreGRPC
	pageSrv.Playtime = coreGRPC
	pageSrv.DB = db
	pageSrv.Media = mediaSvc
	pageSrv.Sessions = sessions
	pageSrv.Production = cfg.Environment == "production"
	pageSrv.AdminAccountIDs = cfg.AdminAccountIDs
	pageSrv.AuthLimit = ratelimit.New(20, time.Minute)
	pageSrv.MediaLimit = ratelimit.New(5, time.Hour)
	pageSrv.MailLimit = accounts.NewMailLimit()
	pageSrv.MinAge = cfg.MinAge
	pageSrv.Turnstile = turnstile.New(cfg.TurnstileSiteKey, cfg.TurnstileSecret)
	if pageSrv.Turnstile == nil {
		log.Warn("turnstile: WEBSITE_TURNSTILE_SITE_KEY/WEBSITE_TURNSTILE_SECRET not set; signup and password-reset bot checks are off")
	}
	log.Info("signup minimum age", "years", cfg.MinAge)
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
	apiSrv.Moderation = coreGRPC
	apiSrv.Saves = savesCli
	// Parental controls: nx-baas owns the device records because the console
	// asks it for them; the app reaches them through /api/v1/me/parental-*.
	parentalCli := pctl.New(os.Getenv("NX_BAAS_URL"), os.Getenv("NX_BAAS_INTERNAL_KEY"))
	if parentalCli == nil {
		log.Info("parental controls: NX_BAAS_URL not set; the page says so and the API answers 503")
	}
	apiSrv.Parental = parentalCli
	pageSrv.Parental = parentalCli
	apiSrv.SaveTitle = pages.SaveTitle
	apiSrv.Chat = chatCli
	apiSrv.AdminIDs = cfg.AdminAccountIDs
	apiSrv.MailLimit = pageSrv.MailLimit // one mail budget per address across site and app
	apiSrv.MinAge = cfg.MinAge
	apiSrv.Turnstile = pageSrv.Turnstile // one key pair for the forms, the app and the widget page

	// The network profile (PRD prds/network-profile-service-prd.md): generated from
	// the live Traefik routing, so adding a title is a server deploy, not six
	// client releases. The file is mounted read-only; without it there is
	// nothing to generate from and /api/v1/network/* answers 503 while the
	// rest of the site is unaffected.
	//
	// The signed redirect ceiling (docs/signed-ceiling.md) is served from a
	// file the owner signs off the box and scp's in; it is re-read on change.
	// Without a verified file the endpoint answers 503 and profiles are
	// bounded by the compiled fallback.
	ceiling := netprofile.NewCeilingStore(cfg.NetProfileCeilingFile, nil, log)
	ceiling.Current() // log the state at startup
	apiSrv.Ceiling = ceiling
	if cfg.NetProfileTraefikFile != "" {
		np, err := netprofile.New(netprofile.Options{
			Ceiling:     ceiling,
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
	var pushSender push.Sender // nil unless FCM is configured; a typed nil must not reach it
	if fcm, err := push.NewFCM(cfg.FCMCredentialsFile); err != nil {
		return err
	} else if fcm == nil {
		log.Info("push: no FCM credentials configured; notifications are disabled")
	} else {
		notifier := push.NewNotifier(coreGRPC, coreGRPC, coreGRPC, db, fcm, log)
		go notifier.Run(ctx)
		pushOn = true
		pushSender = fcm
		log.Info("push: notifier started")
	}

	// Help tickets and crash-report files live on disk under
	// WEBSITE_ATTACHMENTS_DIR. If the directory cannot be made, tickets still
	// work without files rather than taking the site down.
	files, err := attachments.New(cfg.AttachmentsDir)
	if err != nil {
		log.Warn("attachments unavailable; tickets and crash reports carry no files", "dir", cfg.AttachmentsDir, "err", err)
		files = nil
	}
	supportSvc := &support.Service{DB: db, Files: files, Push: pushSender, Log: log}

	// Account erasure: once a closed account's grace period is over, erase it
	// in every service, then in the core. Saves, chat and mods must be
	// configured for an account to finish: an unconfigured service fails its
	// step, and the account waits (closed, not restorable) until it is.
	finaliser := &erasure.Finaliser{Core: coreGRPC, Lock: db, Interval: cfg.ErasureInterval, Log: log,
		Steps: []erasure.Step{
			{Name: "saves", Erase: savesCli.WipeAccount},
			{Name: "chat", Erase: chatCli.EraseAccount},
			erasure.ModsStep(os.Getenv("MODS_INTERNAL_URL"), os.Getenv("MODS_INTERNAL_KEY"), &http.Client{Timeout: time.Minute}),
			erasure.WebsiteStep(db, files, mediaSvc, log),
		}}
	go finaliser.Run(ctx)
	log.Info("erasure: finaliser started", "every", cfg.ErasureInterval)
	pageSrv.Support = supportSvc
	apiSrv.Support = supportSvc
	apiSrv.Crashes = files
	adminSrv := admin.New(tmpl, coreGRPC, db, sessions, cfg.AdminAccountIDs, log)
	adminSrv.Federation = coreGRPC
	adminSrv.Operators = db
	adminSrv.Moderation = coreGRPC
	adminSrv.Erasure = coreGRPC
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
	adminSrv.Support = supportSvc
	adminSrv.CrashFiles = files
	// Console news (the Switch News tab) and the homebrew store: each section
	// exists only when its service is configured.
	adminSrv.ConsoleNews = admin.NewConsoleNewsAPI(os.Getenv("NEWS_ADMIN_URL"), os.Getenv("NEWS_ADMIN_KEY"))
	storeKey := os.Getenv("STORE_ADMIN_KEY")
	if storeKey == "" {
		storeKey = os.Getenv("NX_INTERNAL_KEY") // the same key nx-baas checks
	}
	adminSrv.Store = admin.NewStoreAPI(os.Getenv("STORE_ADMIN_URL"), storeKey)

	// News: the public feed for the landing page, and the operator gRPC for
	// /admin/news. Either can be absent; the landing page then has no news
	// section and the admin page says the service is not configured.
	pageSrv.News = news.NewFeed(os.Getenv("NEWS_URL"))
	apiSrv.News = pageSrv.News // GET /api/v1/news, for the app
	apiSrv.BCAT, err = news.NewBCATProxy(os.Getenv("BCAT_URL"))
	if err != nil {
		return err
	}
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
	// The internal services behind the site, for status.openpak.org: it watches from another host
	// and can only reach what is public. Up or down only, nothing else about them.
	// ponytail: one fixed map; add a service here when the status page should watch it.
	behind := map[string]healthChecker{}
	if savesCli != nil {
		behind["saves"] = savesCli
	}
	if chatCli != nil {
		behind["chat"] = chatCli
	}
	mux.HandleFunc("GET /healthz/{service}", healthzBehind(behind))

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

type healthChecker interface{ Healthy(context.Context) error }

// healthzBehind answers GET /healthz/{service} for the services in behind: 200 when the service's
// own health check passes, 503 when it fails, 404 for anything not listed.
func healthzBehind(behind map[string]healthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("service")
		h, ok := behind[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := h.Healthy(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, `{"status":"down","service":%q}`, name)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q}`, name)
	}
}
