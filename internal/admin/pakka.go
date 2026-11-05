package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
)

// The Pakka approval queue (/admin/pakka). Pakka is the publisher and the official OpenVerse account.
// Templated factual posts publish by themselves; anything the model wrote waits here for a staff
// member to publish it as it is, edit it first, or reject it with a reason. Pakka owns the queue, the
// scopes, the kill switch and the audit log (its docs/publisher.md); this page is a front for its
// publisher admin API (PAKKA_URL + PAKKA_PUBLISH_ADMIN_KEY) and adds nothing of its own to publish with.

// PakkaAPI is Pakka's publisher admin API; nil hides the section.
type PakkaAPI struct {
	URL, Key string
	HTTP     *http.Client
}

// NewPakkaAPI returns nil when PAKKA_URL or the publisher admin key is unset.
func NewPakkaAPI(baseURL, key string) *PakkaAPI {
	if baseURL == "" || key == "" {
		return nil
	}
	return &PakkaAPI{URL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

type pakkaAction struct {
	Tool        string `json:"tool"`
	Category    string `json:"category"`
	Body        string `json:"body"`
	Template    string `json:"template"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
	Discuss     bool   `json:"discuss"`
	SendAsNews  bool   `json:"send_as_news"`
	Community   struct {
		ID      string `json:"id"`
		TitleID string `json:"title_id"`
		Name    string `json:"name"`
	} `json:"community"`
	Channels []string `json:"channels"`
}

type pakkaResult struct {
	Status string            `json:"status"`
	Detail string            `json:"detail"`
	Refs   map[string]string `json:"refs"`
}

type pakkaDraft struct {
	ID           string       `json:"id"`
	Action       pakkaAction  `json:"action"`
	Original     *pakkaAction `json:"original"`
	Preview      string       `json:"preview"`
	CreatedBy    string       `json:"created_by"`
	CreatedAt    time.Time    `json:"created_at"`
	Status       string       `json:"status"`
	Reviewer     string       `json:"reviewer"`
	EditedBy     string       `json:"edited_by"`
	DecidedAt    *time.Time   `json:"decided_at"`
	Result       *pakkaResult `json:"result"`
	Error        string       `json:"error"`
	RejectReason string       `json:"reject_reason"`
}

type pakkaAudit struct {
	Time     time.Time `json:"time"`
	Actor    string    `json:"actor"`
	Event    string    `json:"event"`
	Tool     string    `json:"tool"`
	Category string    `json:"category"`
	DraftID  string    `json:"draft_id"`
	Detail   string    `json:"detail"`
}

type pakkaStatus struct {
	Killed       bool           `json:"killed"`
	Configured   bool           `json:"configured"`
	CategoriesOn []string       `json:"categories_on"`
	AutoCategory []string       `json:"auto_categories"`
	Queue        map[string]int `json:"queue"`
}

func (p *PakkaAPI) call(ctx context.Context, by, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.URL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Admin-Key", p.Key)
	req.Header.Set("X-Approver", by)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&e)
		return &apiError{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func (p *PakkaAPI) Status(ctx context.Context, by string) (pakkaStatus, error) {
	var st pakkaStatus
	return st, p.call(ctx, by, http.MethodGet, "/v1/publisher/status", nil, &st)
}

func (p *PakkaAPI) Drafts(ctx context.Context, by, status string) ([]pakkaDraft, error) {
	var out struct {
		Drafts []pakkaDraft `json:"drafts"`
	}
	err := p.call(ctx, by, http.MethodGet, "/v1/publisher/drafts?status="+url.QueryEscape(status), nil, &out)
	return out.Drafts, err
}

func (p *PakkaAPI) Draft(ctx context.Context, by, id string) (pakkaDraft, []pakkaAudit, error) {
	var out struct {
		Draft   pakkaDraft   `json:"draft"`
		History []pakkaAudit `json:"history"`
	}
	err := p.call(ctx, by, http.MethodGet, "/v1/publisher/drafts/"+url.PathEscape(id), nil, &out)
	return out.Draft, out.History, err
}

// pakkaEdit is the reviewer's change to the free text; empty fields stay as written.
type pakkaEdit struct {
	Body        string `json:"body,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

func (p *PakkaAPI) Approve(ctx context.Context, by, id string, e pakkaEdit) error {
	return p.call(ctx, by, http.MethodPost, "/v1/publisher/drafts/"+url.PathEscape(id)+"/approve", e, nil)
}

func (p *PakkaAPI) Reject(ctx context.Context, by, id, reason string) error {
	return p.call(ctx, by, http.MethodPost, "/v1/publisher/drafts/"+url.PathEscape(id)+"/reject", map[string]string{"reason": reason}, nil)
}

func (p *PakkaAPI) Kill(ctx context.Context, by string) error {
	return p.call(ctx, by, http.MethodPost, "/v1/publisher/kill", nil, nil)
}

// --- views -----------------------------------------------------------------

type pakkaRow struct {
	pakkaDraft
	Kind      string // what it is, in words
	Target    string // where it goes
	Editable  bool
	Label     string // status label
	StatusCSS string
}

type pakkaView struct {
	base
	Failed   bool
	Status   pakkaStatus
	Rows     []pakkaRow
	All      bool
	Pending  int
	Disabled bool // kill switch on or nothing configured: approving is refused
}

type pakkaDraftView struct {
	base
	Failed  bool
	Row     pakkaRow
	History []pakkaAudit
	Text    string // the editable text
	Title   string
	Desc    string
	CanAct  bool
}

func kindLabel(a pakkaAction) string {
	switch a.Tool {
	case "openverse.post":
		return i18n.T("en", "admin.pakka.kind.post")
	case "openverse.community":
		return i18n.T("en", "admin.pakka.kind.community")
	case "news.publish":
		return i18n.T("en", "admin.pakka.kind.news")
	case "boss.notify":
		return i18n.T("en", "admin.pakka.kind.boss")
	}
	return a.Tool
}

func targetLabel(a pakkaAction) string {
	switch a.Tool {
	case "openverse.post":
		t := a.Community.Name
		if t == "" && a.Community.TitleID != "" {
			t = "title " + a.Community.TitleID
		}
		if t == "" {
			t = a.Community.ID
		}
		if a.SendAsNews {
			t += " + News"
		}
		return t
	case "news.publish":
		if len(a.Channels) > 0 {
			return strings.Join(a.Channels, ", ")
		}
		return i18n.T("en", "admin.pakka.target.fitting")
	}
	return ""
}

func statusLabel(st string) (string, string) {
	switch st {
	case "pending":
		return i18n.T("en", "admin.pakka.status.pending"), "warn"
	case "approving":
		return i18n.T("en", "admin.pakka.status.approving"), "warn"
	case "published":
		return i18n.T("en", "admin.pakka.status.published"), "ok"
	case "rejected":
		return i18n.T("en", "admin.pakka.status.rejected"), "bad"
	case "failed":
		return i18n.T("en", "admin.pakka.status.failed"), "bad"
	}
	return st, ""
}

func rowOf(d pakkaDraft) pakkaRow {
	l, css := statusLabel(d.Status)
	return pakkaRow{pakkaDraft: d, Kind: kindLabel(d.Action), Target: targetLabel(d.Action), Label: l, StatusCSS: css,
		Editable: d.Action.Template == "" && d.Action.Tool != "boss.notify" && (d.Status == "pending" || d.Status == "failed")}
}

// approver is who the audit log names: the staff member's name, never the admin key.
func approverName(displayName, accountID string) string {
	n := strings.TrimSpace(displayName)
	if n == "" {
		n = accountID
	}
	return n
}

func pakkaFlash(code string) string {
	switch code {
	case "approved":
		return i18n.T("en", "admin.pakka.flash.approved")
	case "rejected":
		return i18n.T("en", "admin.pakka.flash.rejected")
	case "killed":
		return i18n.T("en", "admin.pakka.flash.killed")
	}
	return ""
}

func (s *Server) handlePakka(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.Pakka == nil {
		http.NotFound(w, r)
		return
	}
	by := approverName(s.adminUser(r, ws).DisplayName, ws.AccountID)
	v := pakkaView{base: s.page(r, ws, i18n.T("en", "admin.pakka")), All: r.URL.Query().Get("all") == "1"}
	v.Flash = pakkaFlash(r.URL.Query().Get("m"))
	st, err := s.Pakka.Status(r.Context(), by)
	if err != nil {
		s.log.Warn("pakka status", "err", err)
		v.Failed = true
	} else {
		v.Status = st
		v.Pending = st.Queue["pending"]
		v.Disabled = st.Killed || !st.Configured
		want := "pending"
		if v.All {
			want = "all"
		}
		drafts, err := s.Pakka.Drafts(r.Context(), by, want)
		if err != nil {
			s.log.Warn("pakka drafts", "err", err)
			v.Failed = true
		}
		// newest first reads best in a log; the queue is oldest first so nothing waits behind newer posts.
		for _, d := range drafts {
			v.Rows = append(v.Rows, rowOf(d))
		}
		if v.All {
			for i, j := 0, len(v.Rows)-1; i < j; i, j = i+1, j-1 {
				v.Rows[i], v.Rows[j] = v.Rows[j], v.Rows[i]
			}
			if len(v.Rows) > 100 {
				v.Rows = v.Rows[:100]
			}
		}
	}
	s.render(w, http.StatusOK, "admin_pakka.html", v)
}

func (s *Server) handlePakkaDraft(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.Pakka == nil {
		http.NotFound(w, r)
		return
	}
	by := approverName(s.adminUser(r, ws).DisplayName, ws.AccountID)
	v := pakkaDraftView{base: s.page(r, ws, i18n.T("en", "admin.pakka"))}
	v.Path = "/admin/pakka"
	d, hist, err := s.Pakka.Draft(r.Context(), by, r.PathValue("id"))
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.Warn("pakka draft", "err", err)
		v.Failed = true
	}
	v.Row = rowOf(d)
	v.History = hist
	v.Text, v.Title, v.Desc = d.Action.Body, d.Action.Title, d.Action.Description
	v.CanAct = d.Status == "pending" || d.Status == "failed"
	v.Flash = pakkaFlash(r.URL.Query().Get("m"))
	s.render(w, http.StatusOK, "admin_pakka_draft.html", v)
}

// handlePakkaDecide approves (with the textarea's text, which Pakka treats as an edit only when it
// differs from the draft) or rejects. Every decision is also written to the website's own audit log.
func (s *Server) handlePakkaDecide(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Pakka == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	by := approverName(s.adminUser(r, ws).DisplayName, ws.AccountID)
	back := "/admin/pakka/" + url.PathEscape(id)
	switch r.PathValue("action") {
	case "approve":
		cur, _, err := s.Pakka.Draft(r.Context(), by, id)
		if err != nil {
			s.log.Warn("pakka draft", "err", err)
			http.Error(w, "Pakka did not answer", http.StatusBadGateway)
			return
		}
		var e pakkaEdit
		if cur.Action.Template == "" {
			if b := strings.TrimSpace(r.PostFormValue("body")); b != "" && b != strings.TrimSpace(cur.Action.Body) {
				e.Body = b
			}
			if t := strings.TrimSpace(r.PostFormValue("title")); t != "" && t != cur.Action.Title {
				e.Title = t
			}
			if d := strings.TrimSpace(r.PostFormValue("description")); d != "" && d != cur.Action.Description {
				e.Description = d
			}
		}
		if err := s.Pakka.Approve(r.Context(), by, id, e); err != nil {
			s.log.Warn("pakka approve", "draft", id, "err", err)
			s.pakkaRefused(w, r, ws.AccountID, by, id, "approve", err)
			return
		}
		detail := "approved"
		if e != (pakkaEdit{}) {
			detail = "approved with edits"
		}
		_ = s.DB.AddAudit(r.Context(), ws.AccountID, "pakka.approve", fmt.Sprintf("draft=%s %s", id, detail))
		http.Redirect(w, r, back+"?m=approved", http.StatusSeeOther)
	case "reject":
		reason := strings.TrimSpace(r.PostFormValue("reason"))
		if reason == "" {
			http.Error(w, i18n.T("en", "admin.pakka.reason.required"), http.StatusBadRequest)
			return
		}
		if err := s.Pakka.Reject(r.Context(), by, id, reason); err != nil {
			s.log.Warn("pakka reject", "draft", id, "err", err)
			s.pakkaRefused(w, r, ws.AccountID, by, id, "reject", err)
			return
		}
		_ = s.DB.AddAudit(r.Context(), ws.AccountID, "pakka.reject", fmt.Sprintf("draft=%s reason=%q", id, reason))
		http.Redirect(w, r, back+"?m=rejected", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

// pakkaRefused tells the staff member what Pakka said (kill switch, category off, already decided).
func (s *Server) pakkaRefused(w http.ResponseWriter, r *http.Request, accountID, by, id, what string, err error) {
	msg := "Pakka did not accept that."
	status := http.StatusBadGateway
	var ae *apiError
	if errors.As(err, &ae) {
		msg = "Pakka refused: " + ae.Error()
		status = http.StatusConflict
		if ae.Status >= 500 && ae.Status != http.StatusServiceUnavailable {
			status = http.StatusBadGateway
		}
	}
	_ = s.DB.AddAudit(r.Context(), accountID, "pakka."+what+".refused", fmt.Sprintf("draft=%s %s", id, msg))
	http.Error(w, msg, status)
}

// handlePakkaKill engages Pakka's kill switch. It cannot be released from here by design: only
// Pakka's environment and a restart release it (docs/publisher.md).
func (s *Server) handlePakkaKill(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.Pakka == nil {
		http.NotFound(w, r)
		return
	}
	if r.PostFormValue("confirm") != "1" {
		http.Error(w, i18n.T("en", "admin.pakka.kill.confirm"), http.StatusBadRequest)
		return
	}
	by := approverName(s.adminUser(r, ws).DisplayName, ws.AccountID)
	if err := s.Pakka.Kill(r.Context(), by); err != nil {
		s.log.Warn("pakka kill", "err", err)
		http.Error(w, "Pakka did not answer", http.StatusBadGateway)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "pakka.kill", "kill switch engaged")
	http.Redirect(w, r, "/admin/pakka?m=killed", http.StatusSeeOther)
}
