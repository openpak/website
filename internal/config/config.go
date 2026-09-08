// Package config loads website configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the website process configuration. Defaults follow the OpenPak
// port plan (ports.md): website block 20010 — HTTP +0, metrics/pprof +2.
type Config struct {
	HTTPAddr    string // listen address for the public server
	MetricsAddr string // metrics/pprof listener

	CoreBaseURL       string        // account core HTTP base (for /v1/* proxies)
	CoreGRPCAddr      string        // account core gRPC address
	CoreInternalKey   string        // shared internal gRPC credential (required)
	CoreHTTPTimeout   time.Duration // per-request timeout for core HTTP calls
	SessionCookieName string        // cookie holding the core web session id

	SiteDatabaseURL string // website-owned Postgres (config, tokens, audit, media)
	CoreSessionTTL  time.Duration

	SessionSecret string // >=32 bytes; CSRF and bearer-token derivation
	Environment   string // development | production

	AdminAccountIDs []string // account ids allowed to open /admin (fail closed)

	// FCMCredentialsFile is a Firebase service-account JSON. Empty means push
	// is not configured and the notifier does not run; the rest of the site is
	// unaffected. Note that iOS delivery additionally needs an APNs key
	// uploaded to the Firebase project, which requires an Apple Developer
	// account — without it FCM reaches Android only.
	FCMCredentialsFile string

	MediaDir   string // filesystem storage root for avatars
	SeedTitles bool   // seed the supported-titles table with defaults on boot
}

// Load reads the environment and validates required fields.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:           env("WEBSITE_HTTP_ADDR", ":20010"),
		MetricsAddr:        env("WEBSITE_METRICS_ADDR", ":20012"),
		CoreBaseURL:        env("CORE_BASE_URL", "http://localhost:20000"),
		CoreGRPCAddr:       env("CORE_GRPC_ADDR", "localhost:20001"),
		CoreInternalKey:    os.Getenv("CORE_INTERNAL_KEY"),
		CoreHTTPTimeout:    10 * time.Second,
		SessionCookieName:  "opak_session",
		SiteDatabaseURL:    os.Getenv("WEBSITE_DATABASE_URL"),
		FCMCredentialsFile: os.Getenv("FCM_CREDENTIALS_FILE"),
		CoreSessionTTL:     30 * 24 * time.Hour,
		SessionSecret:      os.Getenv("WEBSITE_SESSION_SECRET"),
		Environment:        env("WEBSITE_ENVIRONMENT", "development"),
		MediaDir:           env("WEBSITE_MEDIA_DIR", "storage/media"),
		SeedTitles:         env("WEBSITE_SEED_TITLES", "") != "",
	}
	for _, id := range strings.Split(os.Getenv("WEBSITE_ADMIN_ACCOUNT_IDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			c.AdminAccountIDs = append(c.AdminAccountIDs, id)
		}
	}
	if c.CoreInternalKey == "" {
		return c, fmt.Errorf("config: CORE_INTERNAL_KEY is required")
	}
	if len(c.SessionSecret) < 32 {
		return c, fmt.Errorf("config: WEBSITE_SESSION_SECRET must be at least 32 bytes")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
