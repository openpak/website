package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// ModsAPI is the mods service's curation API (/internal/*, bearer
// MODS_INTERNAL_KEY). The service holds the packages and the gate; the website
// only relays an operator's decision.
type ModsAPI struct {
	URL, Key string
}

// NewModsAPI returns nil when the service is not configured.
func NewModsAPI(baseURL, key string) *ModsAPI {
	if baseURL == "" || key == "" {
		return nil
	}
	return &ModsAPI{URL: strings.TrimRight(baseURL, "/"), Key: key}
}

// modEntry is one queued submission, as GET /internal/queue lists it.
type modEntry struct {
	ID        string `json:"id"`
	TitleID   string `json:"title_id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Author    string `json:"author"`
	Licence   string `json:"licence"`
	SourceURL string `json:"source_url"`
	Layout    string `json:"layout"`
	Summary   string `json:"summary"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Status    string `json:"status"`
}

func (m *ModsAPI) queue(ctx context.Context) ([]modEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out struct {
		Queue []modEntry `json:"queue"`
	}
	err := getJSON(ctx, m.URL+"/internal/queue", m.Key, &out)
	return out.Queue, err
}

// decide is publish, reject or takedown. The service answers 404 for an
// unknown id and 409 for a move its state machine refuses; either comes back
// with the service's own words.
func (m *ModsAPI) decide(ctx context.Context, id, action, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"reason": reason})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL+"/internal/mods/"+url.PathEscape(id)+"/"+action, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		return fmt.Errorf("%s: %s", resp.Status, e.Error)
	}
	return nil
}

func (s *Server) renderMods(w http.ResponseWriter, r *http.Request, ws store.WebSession, errMsg string, status int) {
	var queue []modEntry
	if s.Mods != nil {
		var err error
		if queue, err = s.Mods.queue(r.Context()); err != nil {
			s.log.Error("mods queue", "err", err)
			errMsg = err.Error()
		}
	}
	b := s.page(r, ws, i18n.T("en", "admin.mods"))
	b.Path = "/admin/mods" // also rendered from a decision POST
	s.render(w, status, "admin_mods.html", struct {
		base
		Configured bool
		Queue      []modEntry
		Error      string
	}{b, s.Mods != nil, queue, errMsg})
}

func (s *Server) handleMods(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	s.renderMods(w, r, ws, "", http.StatusOK)
}

// handleModDecision relays one curation decision and audits it. The reason
// goes to the submitter, so it is recorded here too.
func (s *Server) handleModDecision(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Mods == nil {
		http.Error(w, i18n.T("en", "admin.mods.off"), http.StatusServiceUnavailable)
		return
	}
	action := r.PathValue("action")
	switch action {
	case "publish", "reject", "takedown":
	default:
		http.NotFound(w, r)
		return
	}
	// The takedown form (POST /admin/mods/takedown) names the mod in a field:
	// it acts on published mods, which are not in the queue.
	id := r.PathValue("id")
	if id == "" {
		id = strings.TrimSpace(r.PostFormValue("id"))
	}
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if id == "" {
		s.renderMods(w, r, ws, i18n.T("en", "admin.mods.noid"), http.StatusBadRequest)
		return
	}
	if err := s.Mods.decide(r.Context(), id, action, reason); err != nil {
		s.renderMods(w, r, ws, err.Error(), http.StatusBadGateway)
		return
	}
	detail := id
	if reason != "" {
		detail += fmt.Sprintf(" reason=%q", reason)
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "mods."+action, detail)
	http.Redirect(w, r, "/admin/mods", http.StatusSeeOther)
}
