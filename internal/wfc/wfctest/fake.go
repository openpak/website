// Package wfctest is an in-memory wfc-api pairing server for tests.
package wfctest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openpak/website/internal/wfc"
)

// Code is one live pairing code: the 8-symbol code and its 5-digit number (as the console shows
// it) name one console. A number resolves only with Proof true (the console's network or name).
type Code struct {
	Number int
	Proof  func(clientIP, name string) bool
	UserID uint64
	Unit   string
	Game   string
	Name   string
}

// Console is a linked console.
type Console struct {
	UserID  uint64
	Account string
	LinkID  string
	Unit    string
}

// Fake answers /api/pair/{preview,lookup,approve,consoles,unlink} with secret "s3".
type Fake struct {
	Mu       sync.Mutex
	Codes    map[string]Code // canonical code -> console
	Linked   map[uint64]Console
	Unlinked []string // link ids
	Erased   []uint64
	Accounts []string // core_account_id of every approve
	Seen     []string // "client_ip|name" of every preview and approve
}

func norm(raw string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(raw)))
}

func (f *Fake) find(raw, ip, name string) (string, Code, bool) {
	n := norm(raw)
	if c, ok := f.Codes[n]; ok {
		return n, c, true
	}
	if num, err := strconv.Atoi(n); err == nil {
		for k, c := range f.Codes {
			if c.Number == num && (c.Proof == nil || c.Proof(ip, name)) {
				return k, c, true
			}
		}
	}
	return "", Code{}, false
}

// New starts the fake and returns a client for it.
func New(t *testing.T) (*Fake, *wfc.Client) {
	f := &Fake{Codes: map[string]Code{}, Linked: map[uint64]Console{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.Mu.Lock()
		defer f.Mu.Unlock()
		var req struct {
			Secret  string `json:"secret"`
			Code    string `json:"code"`
			Account string `json:"core_account_id"`
			LinkID  string `json:"link_id"`
			Erase   bool   `json:"erase"`
			IP      string `json:"client_ip"`
			Name    string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodPost || req.Secret != "s3" {
			http.Error(w, `{"error":"failed_authentication"}`, http.StatusUnauthorized)
			return
		}
		miss := func() { http.Error(w, `{"error":"invalid_pairing_code"}`, http.StatusNotFound) }
		spec := func(c Code, already bool) map[string]any {
			return map[string]any{"unit": c.Unit, "gsbrcd": c.Game, "name": c.Name, "expires_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339), "status": "linked", "already_linked": already}
		}
		switch r.URL.Path {
		case "/api/pair/preview", "/api/pair/lookup":
			f.Seen = append(f.Seen, req.IP+"|"+req.Name)
			_, c, ok := f.find(req.Code, req.IP, req.Name)
			if !ok {
				miss()
				return
			}
			_ = json.NewEncoder(w).Encode(spec(c, false))
		case "/api/pair/approve":
			f.Seen = append(f.Seen, req.IP+"|"+req.Name)
			k, c, ok := f.find(req.Code, req.IP, req.Name)
			if !ok || req.Account == "" {
				miss()
				return
			}
			f.Accounts = append(f.Accounts, req.Account)
			if l, ok := f.Linked[c.UserID]; ok {
				if l.Account != req.Account {
					http.Error(w, `{"error":"conflict"}`, http.StatusConflict)
					return
				}
				_ = json.NewEncoder(w).Encode(spec(c, true))
				return
			}
			delete(f.Codes, k)
			f.Linked[c.UserID] = Console{UserID: c.UserID, Account: req.Account, LinkID: "wfclink-" + strconv.Itoa(len(f.Accounts)), Unit: c.Unit}
			_ = json.NewEncoder(w).Encode(spec(c, false))
		case "/api/pair/consoles":
			out := []map[string]any{}
			for _, l := range f.Linked {
				if l.Account == req.Account {
					out = append(out, map[string]any{"link_id": l.LinkID, "unit": l.Unit, "games": []string{"RMCP"}, "linked_at": time.Now().UTC().Format(time.RFC3339)})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"consoles": out})
		case "/api/pair/unlink":
			for uid, l := range f.Linked {
				if l.Account == req.Account && l.LinkID == req.LinkID {
					delete(f.Linked, uid)
					f.Unlinked = append(f.Unlinked, l.LinkID)
					if req.Erase {
						f.Erased = append(f.Erased, uid)
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			http.Error(w, `{"error":"invalid_query"}`, http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, wfc.New(srv.URL, "s3")
}
