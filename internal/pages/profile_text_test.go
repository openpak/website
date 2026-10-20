package pages

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestProfileFormEditsAndClearsCoreText(t *testing.T) {
	s, fc, _ := newTestServer(t)
	email, _ := seedTwoAccounts(fc)
	a := login(t, s, email, "password-A-1")
	rec := authedRequest(s, a, http.MethodPost, "/account/profile", &url.Values{
		"bio": {"I play <games>"}, "motto": {"Play together"},
	})
	if !strings.Contains(rec.Header().Get("Location"), "profile-saved") {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String()
	if !strings.Contains(body, `value="Play together"`) || !strings.Contains(body, "I play &lt;games&gt;") {
		t.Fatal("profile form did not render escaped core text")
	}
	authedRequest(s, a, http.MethodPost, "/account/profile", &url.Values{"bio": {""}, "motto": {""}})
	if fc.profileText.Bio == nil || *fc.profileText.Bio != "" || fc.profileText.Motto == nil || *fc.profileText.Motto != "" {
		t.Fatal("cleared fields were not sent to core")
	}
}
