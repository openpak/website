package pages

import (
	"net/http/httptest"
	"testing"
)

func TestHandleLangStaysOnSite(t *testing.T) {
	s, _, _ := newTestServer(t)
	for back, want := range map[string]string{
		"/titles":           "/titles",
		"//evil.test/x":     "/",
		"/\\evil.test":      "/",
		"https://evil.test": "/",
		"":                  "/",
	} {
		w := httptest.NewRecorder()
		s.handleLang(w, httptest.NewRequest("GET", "/lang?l=en&back="+back, nil))
		if got := w.Header().Get("Location"); got != want {
			t.Errorf("back=%q: Location %q, want %q", back, got, want)
		}
		if c := w.Result().Cookies(); len(c) != 1 || c[0].Value != "en" {
			t.Errorf("back=%q: cookies %v", back, c)
		}
	}
}
