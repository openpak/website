package pages

import (
	"context"
	"regexp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The alpha strip and the legal links are on every page of the site shell.
func TestAlphaStripAndLegalFooter(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, path := range []string{"/", "/titles", "/features", "/downloads", "/status", "/login", "/register", "/forgot-password", "/help", "/terms", "/privacy", "/legal"} {
		rec := get(t, s, path)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		for _, want := range []string{`class="alpha-strip"`, "OpenPak is in public alpha", `href="/terms"`, `href="/privacy"`, `href="/legal"`, "mailto:contact@openpak.org", "SUPPORT.md"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
	}
}

func TestLegalPages(t *testing.T) {
	s, _, _ := newTestServer(t)
	for path, want := range map[string][]string{
		"/terms":   {"Terms of use", "the OpenPak project", "at least 16", "AGPL", "pirated"},
		"/privacy": {"Privacy policy", "the OpenPak project", "opak_session", "Cloudflare", "Oracle Cloud", "Zoho Mail", "smtp.zoho.eu", "federation node operators", "GitHub", "essential cookies only", "Delete account", "contact@openpak.org", "deleted user", "Backups are kept for up to 14 days"},
		"/legal":   {"Nintendo", "Microsoft", "Sony", "takedown", "contact@openpak.org", "Name and logo", "based on OpenPak software"},
	} {
		body := get(t, s, path).Body.String()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", path, w)
			}
		}
		if strings.Contains(body, "may set its own") {
			t.Errorf("%s: openpak.org sets no Cloudflare cookie", path)
		}
		if strings.Contains(body, "OWNER REVIEW") {
			t.Errorf("%s: the review note must stay in the template, not reach visitors", path)
		}
	}
}

func TestRegisterNotices(t *testing.T) {
	s, _, _ := newTestServer(t)
	body := get(t, s, "/register").Body.String()
	if !strings.Contains(body, `notice warn`) || !strings.Contains(body, "you agree to the Terms of use") {
		t.Error("the register form should carry the alpha notice and the terms agreement")
	}
	if strings.Contains(get(t, s, "/").Body.String(), "opened or closed by the operator") {
		t.Error("the landing page still has operator-speak")
	}
}

func TestSecurityTxt(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := get(t, s, "/.well-known/security.txt")
	body := rec.Body.String()
	for _, want := range []string{"Contact: mailto:contact@openpak.org", "Expires: 2027-", "Policy: https://github.com/openpak/.github/blob/main/SECURITY.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("security.txt: missing %q", want)
		}
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Error("security.txt must be text/plain")
	}
}

func TestHelpContactSignedOut(t *testing.T) {
	s, _, _ := newTestServer(t)
	body := get(t, s, "/help").Body.String()
	if !strings.Contains(body, `<a href="mailto:contact@openpak.org">contact@openpak.org</a>`) {
		t.Error("signed-out help should show the contact address")
	}
}

// Platform badges and status-page networks come from the /features rows.
func TestConsoleStagesFollowFeatures(t *testing.T) {
	s, _, db := newTestServer(t)
	_ = db.SeedRequests(context.Background(), []store.Request{
		{Kind: "feature", Name: "Sign-in", Console: "Switch", Status: "working"},
		{Kind: "feature", Name: "Friends", Console: "Switch", Status: "partial"},
		{Kind: "feature", Name: "Matchmaking", Console: "Wii U / 3DS", Status: "beta"},
		{Kind: "feature", Name: "WFC", Console: "Wii U / 3DS / Wii / DS", Status: "alpha"},
		{Kind: "feature", Name: "Xbox Live", Console: "Xbox 360", Status: "in-development"},
		{Kind: "feature", Name: "Network", Console: "PSP", Status: "planned"},
		{Kind: "feature", Name: "Broken thing", Console: "PS3", Status: "broken"},
	})
	st := consoleStages(mustFeatures(t, db))
	for console, want := range map[string]string{"Switch": "live", "Wii U": "beta", "3DS": "beta", "Wii": "alpha", "DS": "alpha", "Xbox 360": "in-development", "PSP": "planned"} {
		if st[console] != want {
			t.Errorf("%s: %q, want %q", console, st[console], want)
		}
	}
	if _, ok := st["PS3"]; ok {
		t.Error("a broken feature says nothing about how far a console is")
	}
	byName := map[string]network{}
	for _, n := range networks {
		byName[n.Name] = n
	}
	for name, want := range map[string]string{"Switch": "live", "Wii U / 3DS": "beta", "Wii / DS": "building", "Xbox 360": "building", "PlayStation 3": "building", "Xbox": "building", "PSP": "planned"} {
		if got := byName[name].stateAt(st); got != want {
			t.Errorf("network %s: %q, want %q", name, got, want)
		}
	}
	if body := get(t, s, "/status").Body.String(); !strings.Contains(body, "In development") {
		t.Error("the status page should label Xbox, Xbox 360 and PS3 In development")
	}
	if body := get(t, s, "/features").Body.String(); !strings.Contains(body, ">in development</span>") {
		t.Error("/features should show the in-development badge in words")
	}
	body := get(t, s, "/downloads").Body.String()
	if !strings.Contains(body, "switch-patcher/releases/latest") || !strings.Contains(body, "openpak/app/releases/latest") {
		t.Error("downloads should link the Switch setup app and the phone app")
	}
}

// Without feature rows, nothing unfinished is drawn as live.
func TestFallbackStagesAreHonest(t *testing.T) {
	for _, n := range networks {
		if n.Name != "Switch" && n.State == "live" {
			t.Errorf("%s falls back to live", n.Name)
		}
	}
	for _, p := range platformsAt(nil) {
		if p.Console != "Switch" && p.Status == "live" {
			t.Errorf("%s falls back to live", p.Name)
		}
	}
}

func mustFeatures(t *testing.T, db *store.Memory) []store.Request {
	t.Helper()
	f, err := db.ListRequests(context.Background(), "feature", "")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var idAttr = regexp.MustCompile(`<h[23] id="([a-z-]+)"`)

func getLang(t *testing.T, s *Server, path, lang string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Accept-Language", lang)
	s.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s [%s]: %d", path, lang, rec.Code)
	}
	return rec.Body.String()
}

// Every language renders every legal page as a full translation: the same
// sections, the live values, the contact address and the pointer to the
// authoritative English text.
func TestLegalPagesTranslated(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.Federation = &pagesFed{}
	for _, path := range []string{"/terms", "/privacy", "/legal", "/operators"} {
		en := getLang(t, s, path, "en")
		wantIDs := idAttr.FindAllStringSubmatch(en, -1)
		if len(wantIDs) == 0 {
			t.Fatalf("%s: no sections in English", path)
		}
		if strings.Contains(en, "available in English only") || strings.Contains(en, "legal-translation") {
			t.Errorf("%s: English page carries a translation notice", path)
		}
		for _, lang := range []string{"de", "es", "fr", "it", "pt-BR"} {
			body := getLang(t, s, path, lang)
			notice := i18n.T(lang, "legal.translation")
			if notice == "legal.translation" || !strings.Contains(body, notice) {
				t.Errorf("%s [%s]: authoritative-text notice missing", path, lang)
			}
			if !strings.Contains(body, `href="/lang?l=en&amp;back=`+path+`"`) {
				t.Errorf("%s [%s]: no link to the English version", path, lang)
			}
			if strings.Contains(body, i18n.T(lang, "legal.english")) && i18n.T(lang, "legal.english") != "legal.english" {
				t.Errorf("%s [%s]: still says English only", path, lang)
			}
			if strings.Contains(body, "OWNER REVIEW") || strings.Contains(body, "{{") {
				t.Errorf("%s [%s]: review note or raw template leaked", path, lang)
			}
			if !strings.Contains(body, `<article class="legal" lang="`+lang+`">`) {
				t.Errorf("%s [%s]: article not marked with its language", path, lang)
			}
			if path != "/operators" && !strings.Contains(body, "contact@openpak.org") {
				t.Errorf("%s [%s]: contact address missing", path, lang)
			}
			got := idAttr.FindAllStringSubmatch(body, -1)
			if len(got) != len(wantIDs) {
				t.Errorf("%s [%s]: %d sections, English has %d", path, lang, len(got), len(wantIDs))
				continue
			}
			for i := range got {
				if got[i][1] != wantIDs[i][1] {
					t.Errorf("%s [%s]: section %d is #%s, want #%s", path, lang, i, got[i][1], wantIDs[i][1])
				}
			}
			if strings.Count(body, "<li>") != strings.Count(en, "<li>") {
				t.Errorf("%s [%s]: list items differ from English", path, lang)
			}
			if strings.Count(body, "<a href") < strings.Count(en, "<a href") {
				t.Errorf("%s [%s]: links missing", path, lang)
			}
			if path == "/operators" {
				for _, v := range []string{"160", "1000", "99.5", "mailto:contact@openpak.org"} {
					if !strings.Contains(body, v) {
						t.Errorf("%s [%s]: live value %q missing", path, lang, v)
					}
				}
				for _, w := range []string{"72", "3 "} {
					if !strings.Contains(body, w) {
						t.Errorf("%s [%s]: %q missing", path, lang, w)
					}
				}
			}
			if path == "/privacy" {
				for _, v := range []string{"opak_session", "Cloudflare", "Oracle Cloud", "Zoho Mail", "GitHub", "16"} {
					if !strings.Contains(body, v) {
						t.Errorf("%s [%s]: %q missing", path, lang, v)
					}
				}
			}
		}
	}
}

// Register carries the minimum age in every language.
func TestRegisterMinAgeTranslated(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, lang := range []string{"de", "es", "fr", "it", "pt-BR"} {
		body := getLang(t, s, "/register", lang)
		if !strings.Contains(body, i18n.T(lang, "auth.register.agree")) {
			t.Errorf("register [%s]: terms agreement not translated", lang)
		}
	}
}
