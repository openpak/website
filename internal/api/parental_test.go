package api

// The relay's two jobs: never let one account read another's consoles, and say
// "not configured" differently from "the service broke".

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
