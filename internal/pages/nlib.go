package pages

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// nlib is the public Switch title database the saves page names games and draws icons from.
// Icons go through this site rather than straight to the browser, so which games a player
// has saves for never reaches a third party from their browser, and the CSP stays 'self'.
var nlibBase = "https://api.nlib.cc/nx/"

var (
	nlibHTTP  = &http.Client{Timeout: 3 * time.Second}
	nlibNames sync.Map // title id -> name; "" is a known miss
	switchID  = regexp.MustCompile(`^[0-9A-Fa-f]{16}$`)
	// Published icons for titles the upstream database does not carry yet.
	switchIconDir = filepath.Join("web", "static", "art", "switch")
)

// nlibTitleID maps an update title to its base game, where nlib stores the
// name and icon. Catalogue entries can identify the installed update instead.
func nlibTitleID(id string) string {
	id = strings.ToUpper(id)
	if strings.HasSuffix(id, "800") {
		return strings.TrimSuffix(id, "800") + "000"
	}
	return id
}

// switchTitleName is the game's name from nlib, or "" when it has none or is unreachable.
// ponytail: an in-process cache that lives as long as the process; a restart refetches.
func switchTitleName(ctx context.Context, id string) string {
	if !switchID.MatchString(id) {
		return ""
	}
	id = nlibTitleID(id)
	if n, ok := nlibNames.Load(id); ok {
		return n.(string)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nlibBase+id, nil)
	if err != nil {
		return ""
	}
	resp, err := nlibHTTP.Do(req)
	if err != nil {
		return "" // unreachable: not cached, the next page load tries again
	}
	defer resp.Body.Close()
	var t struct{ Name string }
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t)
	} else if resp.StatusCode != http.StatusNotFound {
		return ""
	}
	nlibNames.Store(id, t.Name)
	return t.Name
}

// handleSwitchIcon relays a 256px icon for a Switch title id.
func (s *Server) handleSwitchIcon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !switchID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	id = nlibTitleID(id)
	if f, err := os.Open(filepath.Join(switchIconDir, id+".jpg")); err == nil {
		defer f.Close()
		if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "public, max-age=2592000")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			http.ServeContent(w, r, info.Name(), info.ModTime(), f)
			return
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, nlibBase+id+"/icon/256/256", nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	resp, err := nlibHTTP.Do(req)
	if err != nil {
		http.Error(w, "icon unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "public, max-age=2592000")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
