package api

// The relay's two jobs: never let one account read another's consoles, and say
// "not configured" differently from "the service broke".

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/pctl"
)

func TestParentalRelayCarriesTheSignedInAccountOnly(t *testing.T) {
	var gotPath, gotKey string
	nx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.RequestURI(), r.Header.Get("X-Internal-Key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"devices":[{"moon_id":"00000000000000ff","paired":false,"pairing_code":"ABC234"}]}`))
	}))
	defer nx.Close()

	c := pctl.New(nx.URL, "k")
	devices, err := c.Devices(t.Context(), "acct-parent")
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if gotPath != "/internal/pctl/devices?account_id=acct-parent" {
		t.Fatalf("relayed %q -- the account must be the signed-in one, never a client-supplied id", gotPath)
	}
	if gotKey != "k" {
		t.Fatalf("internal key %q", gotKey)
	}
	if len(devices) != 1 || devices[0].PairingCode != "ABC234" {
		t.Fatalf("devices %+v", devices)
	}
}

func TestParentalPairReturnsTheCode(t *testing.T) {
	nx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/pctl/pairing" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["account_id"] != "acct-parent" || in["display_name"] != "Alexis" {
			t.Errorf("body %v", in)
		}
		w.Write([]byte(`{"moon_id":"00000000000000ff","pairing_code":"ABC234","expires_at":9}`))
	}))
	defer nx.Close()

	p, err := pctl.New(nx.URL, "k").Pair(t.Context(), "acct-parent", "Alexis")
	if err != nil || p.PairingCode != "ABC234" {
		t.Fatalf("pair: %+v %v", p, err)
	}
}

func TestParentalUnconfiguredIsNotAFailure(t *testing.T) {
	var nilClient *pctl.Client
	if _, err := nilClient.Devices(t.Context(), "acct-parent"); err != pctl.ErrUnavailable {
		t.Fatalf("err %v -- an unconfigured deployment must say so, not look broken", err)
	}

	w := httptest.NewRecorder()
	parentalErr(w, pctl.ErrUnavailable)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}
	w = httptest.NewRecorder()
	parentalErr(w, &pctl.Error{Status: 500, Code: "internal"})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502 for a service that answered badly", w.Code)
	}
}

// nxParental fakes nx-baas for one account: its list carries a PIN, and a
// player picture exists only for that account's console.
func nxParental(t *testing.T, account string) *httptest.Server {
	nx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mine := r.URL.Query().Get("account_id") == account
		switch {
		case r.URL.Path == "/internal/pctl/devices":
			if !mine {
				w.Write([]byte(`{"devices":[]}`))
				return
			}
			w.Write([]byte(`{"devices":[{"moon_id":"00000000000000aa","paired":true,"unlock_code":"8759","wrong_pin_attempts":[9,8]}]}`))
		case r.URL.Path == "/internal/pctl/devices/00000000000000aa/players/p1/image" && mine:
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte{0xFF, 0xD8, 0xFF, 0xD9})
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	t.Cleanup(nx.Close)
	return nx
}

// The PIN is never in the list, and it and the pictures answer 404 for a
// console that is not the caller's, the same as for one that does not exist.
func TestParentalPinAndPicturesAreTheCallersOnly(t *testing.T) {
	s, _, _ := newTestAPI(t)
	s.Parental = pctl.New(nxParental(t, "acct-a@x.test").URL, "k")
	tok := signIn(t, s)

	rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls", tok, nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "8759") || !strings.Contains(rec.Body.String(), `"wrong_pin_attempts":[9,8]`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/pin", tok, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"unlock_code\":\"8759\"}\n" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pin: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000bb/pin", tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else's pin: %d", rec.Code)
	}
	rec = do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/players/p1/image", tok, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Body.Len() != 4 {
		t.Fatalf("picture: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000bb/players/p1/image", tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else's picture: %d", rec.Code)
	}
	s.Parental = pctl.New(nxParental(t, "acct-someone-else").URL, "k")
	if rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/pin", tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a console another account manages: %d", rec.Code)
	}
}

// The unlock answers yes or no to the account's password, and stops answering
// after a handful of tries.
func TestParentalUnlockChecksThePassword(t *testing.T) {
	s, _, _ := newTestAPI(t)
	tok := signIn(t, s)
	rec := do(t, s, http.MethodPost, "/api/v1/me/parental-controls/unlock", tok, map[string]string{"password": "wrong"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"wrong_password"`) {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/parental-controls/unlock", tok, map[string]string{"password": "password-A-1"}); rec.Code != http.StatusNoContent {
		t.Fatalf("right password: %d %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 3; i++ {
		do(t, s, http.MethodPost, "/api/v1/me/parental-controls/unlock", tok, map[string]string{"password": "wrong"})
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/me/parental-controls/unlock", tok, map[string]string{"password": "password-A-1"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("sixth try: %d", rec.Code)
	}
}
