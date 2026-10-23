// Package config loads website configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
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

	MediaDir string // filesystem storage root for avatars
	// AttachmentsDir holds help-ticket attachments and crash-report files
	// (WEBSITE_ATTACHMENTS_DIR). Put it on a volume: it is not rebuilt from
	// anywhere.
	AttachmentsDir string

	// ErasureInterval is how often the account-erasure finaliser looks for
	// closed accounts whose grace period is over (WEBSITE_ERASURE_INTERVAL,
	// Go duration, default 1h). The grace period itself is the core's
	// ACCOUNT_DELETION_GRACE.
	ErasureInterval time.Duration

	// NetProfileTraefikFile is the read-only mount of Traefik's dynamic
	// configuration; the network profile (PRD prds/network-profile-service-prd.md) is
	// generated from it at request time. Empty disables /api/v1/network/*:
	// without the live routing there is nothing to generate from.
	NetProfileTraefikFile string
	// NetProfilePlatformMap optionally overrides the embedded per-platform
	// map (platforms.yml). The embedded copy is what production runs; an
	// override is for experiments, not for shipping a change without a
	// reviewed file.
	NetProfilePlatformMap string
	// NetProfileCeilingFile is the signed redirect ceiling envelope
	// (docs/signed-ceiling.md), served verbatim at /api/v1/network/ceiling
	// and re-read when it changes. Missing or invalid: 503 there, and
	// profiles fall back to the compiled Universe.
	NetProfileCeilingFile string

	// TurnstileSiteKey and TurnstileSecret are the Cloudflare Turnstile pair
	// for the signup and password-reset forms. Either unset: no bot check.
	TurnstileSiteKey string
	TurnstileSecret  string
	// MinAge is the youngest age allowed to create an account
	// (WEBSITE_MIN_AGE, default 16, the highest EU digital-consent age); 0 turns
	// the check off.
	MinAge int
}

// Load reads the environment and validates required fields.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:              env("WEBSITE_HTTP_ADDR", ":20010"),
		MetricsAddr:           env("WEBSITE_METRICS_ADDR", ":20012"),
		CoreBaseURL:           env("CORE_BASE_URL", "http://localhost:20000"),
		CoreGRPCAddr:          env("CORE_GRPC_ADDR", "localhost:20001"),
		CoreInternalKey:       os.Getenv("CORE_INTERNAL_KEY"),
		CoreHTTPTimeout:       10 * time.Second,
		SessionCookieName:     "opak_session",
		SiteDatabaseURL:       os.Getenv("WEBSITE_DATABASE_URL"),
		FCMCredentialsFile:    os.Getenv("FCM_CREDENTIALS_FILE"),
		CoreSessionTTL:        30 * 24 * time.Hour,
		SessionSecret:         os.Getenv("WEBSITE_SESSION_SECRET"),
		Environment:           env("WEBSITE_ENVIRONMENT", "development"),
		MediaDir:              env("WEBSITE_MEDIA_DIR", "storage/media"),
		AttachmentsDir:        env("WEBSITE_ATTACHMENTS_DIR", "storage/attachments"),
		NetProfileTraefikFile: os.Getenv("NETPROFILE_TRAEFIK_FILE"),
		NetProfilePlatformMap: os.Getenv("NETPROFILE_PLATFORM_MAP"),
		NetProfileCeilingFile: env("NETPROFILE_CEILING_FILE", "/data/netprofile-ceiling.json"),
		TurnstileSiteKey:      os.Getenv("WEBSITE_TURNSTILE_SITE_KEY"),
		TurnstileSecret:       os.Getenv("WEBSITE_TURNSTILE_SECRET"),
	}
	minAge, err := strconv.Atoi(env("WEBSITE_MIN_AGE", "16"))
	if err != nil || minAge < 0 {
		return c, fmt.Errorf("config: WEBSITE_MIN_AGE must be a whole number of years")
	}
	c.MinAge = minAge
	every, err := time.ParseDuration(env("WEBSITE_ERASURE_INTERVAL", "1h"))
	if err != nil || every < time.Minute {
		return c, fmt.Errorf("config: WEBSITE_ERASURE_INTERVAL must be a duration of at least 1m")
	}
	c.ErasureInterval = every
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
