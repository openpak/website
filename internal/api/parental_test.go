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

// nxParentalWrites fakes nx-baas's write routes for one account's console
// 00000000000000aa, remembering the last body it was sent. Activity answers
// 404 until live is set, as a nx-baas without the route does.
func nxParentalWrites(t *testing.T, account string, live *bool, got *map[string]any) *httptest.Server {
	nx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mine := r.URL.Query().Get("account_id") == account
		const dev = "/internal/pctl/devices/00000000000000aa"
		switch {
		case r.URL.Path == "/internal/pctl/devices":
			if mine {
				w.Write([]byte(`{"devices":[{"moon_id":"00000000000000aa","paired":true}]}`))
				return
			}
			w.Write([]byte(`{"devices":[]}`))
			return
		case !mine:
		case r.Method == http.MethodPut && r.URL.Path == dev+"/setting":
			*got = nil
			json.NewDecoder(r.Body).Decode(got)
			if s, _ := (*got)["setting"].(map[string]any); s["bad"] != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"not in the console's parse table: customSettings.ageRestriction.ratingAge"}`))
				return
			}
			w.Write([]byte(`{"etag":"e1","updated_at":1790514636}`))
			return
		case r.Method == http.MethodDelete && r.URL.Path == dev:
			w.WriteHeader(http.StatusNoContent)
			return
		case r.Method == http.MethodGet && r.URL.Path == dev+"/activity" && *live:
			if r.URL.Query().Get("days") != "7" {
				t.Errorf("days %q", r.URL.Query().Get("days"))
			}
			w.Write([]byte(`{"days":[{"date":"2026-09-27","total_minutes":95,"titles":[{"title_id":"0100e65002bb8000","minutes":60},{"title_id":"01000000000000ff","minutes":5}]},{"date":"2026-09-26","total_minutes":10,"titles":[]}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	t.Cleanup(nx.Close)
	return nx
}

// Every write and the activity answer 404 for a console another account
// manages, the same as for one that does not exist.
func TestParentalWritesAreTheCallersOnly(t *testing.T) {
	s, _, _ := newTestAPI(t)
	live, got := true, map[string]any{}
	s.Parental = pctl.New(nxParentalWrites(t, "acct-someone-else", &live, &got).URL, "k")
	tok := signIn(t, s)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/v1/me/parental-controls/00000000000000aa/setting", map[string]any{"setting": map[string]any{"functionalRestrictionLevel": "NONE"}}},
		{http.MethodPut, "/api/v1/me/parental-controls/00000000000000aa/pin", map[string]string{"unlock_code": "1234"}},
		{http.MethodDelete, "/api/v1/me/parental-controls/00000000000000aa", nil},
		{http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/activity", nil},
	} {
		if rec := do(t, s, c.method, c.path, tok, c.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	live = false
	if rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/activity", tok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("activity before nx-baas serves it, someone else's console: %d", rec.Code)
	}
}

// The document goes through whole, and a refusal comes back with the key.
func TestParentalSettingRelaysTheRefusalsPath(t *testing.T) {
	s, _, _ := newTestAPI(t)
	live, got := false, map[string]any{}
	s.Parental = pctl.New(nxParentalWrites(t, "acct-a@x.test", &live, &got).URL, "k")
	tok := signIn(t, s)
	doc := map[string]any{"functionalRestrictionLevel": "CUSTOM", "whitelistedApplications": map[string]any{"0100e65002bb8000": map[string]any{"safeLaunch": "ALLOW"}}}
	rec := do(t, s, http.MethodPut, "/api/v1/me/parental-controls/00000000000000aa/setting", tok, map[string]any{"setting": doc})
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"etag\":\"e1\",\"updated_at\":1790514636}\n" {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if b, _ := json.Marshal(got["setting"]); !strings.Contains(string(b), "0100e65002bb8000") {
		t.Fatalf("relayed %s", b)
	}
	rec = do(t, s, http.MethodPut, "/api/v1/me/parental-controls/00000000000000aa/setting", tok, map[string]any{"setting": map[string]any{"bad": 1}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_setting"`) ||
		!strings.Contains(rec.Body.String(), `"path":"customSettings.ageRestriction.ratingAge"`) {
		t.Fatalf("refusal: %d %s", rec.Code, rec.Body.String())
	}
}

// A PIN is exactly four digits, refused before it leaves, and never cached.
func TestParentalPinIsFourDigits(t *testing.T) {
	s, _, _ := newTestAPI(t)
	live, got := false, map[string]any{}
	s.Parental = pctl.New(nxParentalWrites(t, "acct-a@x.test", &live, &got).URL, "k")
	tok := signIn(t, s)
	for _, pin := range []string{"123", "12345", "12a4", "１２３４", ""} {
		rec := do(t, s, http.MethodPut, "/api/v1/me/parental-controls/00000000000000aa/pin", tok, map[string]string{"unlock_code": pin})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_pin"`) {
			t.Errorf("%q: %d %s", pin, rec.Code, rec.Body.String())
		}
	}
	rec := do(t, s, http.MethodPut, "/api/v1/me/parental-controls/00000000000000aa/pin", tok, map[string]string{"unlock_code": "0427"})
	if rec.Code != http.StatusNoContent || rec.Header().Get("Cache-Control") != "no-store" || got["unlock_code"] != "0427" {
		t.Fatalf("pin: %d %v", rec.Code, got)
	}
	if rec := do(t, s, http.MethodDelete, "/api/v1/me/parental-controls/00000000000000aa", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove: %d", rec.Code)
	}
}

// Activity names what the catalogue knows, leaves the rest nameless, reads a
// nx-baas without the route as no activity, and bounds days itself.
func TestParentalActivityNamesTitles(t *testing.T) {
	s, _, _ := newTestAPI(t)
	live, got := true, map[string]any{}
	s.Parental = pctl.New(nxParentalWrites(t, "acct-a@x.test", &live, &got).URL, "k")
	tok := signIn(t, s)
	rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/activity?days=7", tok, nil)
	want := `{"days":[{"date":"2026-09-27","total_minutes":95,"titles":[{"title_id":"0100e65002bb8000","name":"Stardew Valley","minutes":60},{"title_id":"01000000000000ff","minutes":5}]},{"date":"2026-09-26","total_minutes":10,"titles":[]}]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
	}
	for _, q := range []string{"0", "31", "x"} {
		if rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/activity?days="+q, tok, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("days=%s: %d", q, rec.Code)
		}
	}
	live = false
	if rec := do(t, s, http.MethodGet, "/api/v1/me/parental-controls/00000000000000aa/activity", tok, nil); rec.Code != http.StatusOK || rec.Body.String() != "{\"days\":[]}\n" {
		t.Fatalf("no activity route yet: %d %s", rec.Code, rec.Body.String())
	}
}
