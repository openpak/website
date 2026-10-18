package pages

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestHandleCannotBeEdited(t *testing.T) {
	s, fc, _ := newTestServer(t)
	fc.accounts["a@x.test"], fc.verified["a@x.test"] = "password-A-1", true
	a := login(t, s, "a@x.test", "password-A-1")
	body := authedRequest(s, a, http.MethodGet, "/account", nil).Body.String()
	if strings.Contains(body, `name="gamertag"`) {
		t.Fatal("handle edit form is still present")
	}
	response := authedRequest(s, a, http.MethodPost, "/account/networks/xbox/gamertag", &url.Values{"gamertag": {"DifferentName"}})
	if response.Code != http.StatusGone {
		t.Fatalf("retired rename: %d %s", response.Code, response.Body.String())
	}
}
