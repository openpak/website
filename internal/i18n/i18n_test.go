package i18n

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every non-admin key must exist in every language, so new copy cannot land
// English-only. Admin pages stay English.
func TestCataloguesComplete(t *testing.T) {
	for lang, c := range languages {
		for key := range languages["en"] {
			if _, ok := c[key]; !ok && !strings.HasPrefix(key, "admin.") {
				t.Errorf("%s: missing %q", lang, key)
			}
		}
	}
}

func TestLang(t *testing.T) {
	for header, want := range map[string]string{
		"":                         "en",
		"ja":                       "en",
		"fr-CA":                    "fr",
		"es-419":                   "es",
		"de-AT,de;q=0.9,en;q=0.8":  "de",
		"en;q=0.5,pt":              "pt-BR",
		"PT-pt":                    "pt-BR",
		"ja, de;q=0.1":             "de",
		"de;q=0, en":               "en",
		"en-GB,en;q=0.9,de;q=0.8":  "en",
		"*;q=0.5, pt-br;q=0.6, xx": "pt-BR",
	} {
		if got := Lang(header); got != want {
			t.Errorf("Lang(%q) = %q, want %q", header, got, want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "de")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "pt-BR"})
	if got := FromRequest(r); got != "pt-BR" {
		t.Errorf("cookie ignored: %q", got)
	}
}

func TestDate(t *testing.T) {
	d := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	for lang, want := range map[string]string{"en": "2 March 2026", "de": "2. März 2026", "pt-BR": "2 de março de 2026"} {
		if got := Date(lang, d); got != want {
			t.Errorf("Date(%s) = %q, want %q", lang, got, want)
		}
	}
	for _, lang := range Supported() {
		if n := len(strings.Fields(T(lang, "date.months"))); n != 12 {
			t.Errorf("%s: %d months", lang, n)
		}
	}
}
