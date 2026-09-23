package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// The homebrew store (/admin/store): titles and their versions, through
// nx-baas's store admin API (nx-baas docs/store-admin-api.md, X-Internal-Key).
// The store serves only homebrew packaged without a rights ID; nx-baas checks
// every upload, and this page only relays. A version upload is streamed from
// the browser to nx-baas part by part and never held here, in memory or on disk.

// StoreAPI is nx-baas's /internal/store/*; nil hides the section.
type StoreAPI struct{ c *keyClient }

// NewStoreAPI returns nil when STORE_ADMIN_URL or its key is unset.
func NewStoreAPI(baseURL, key string) *StoreAPI {
	c := newKeyClient(baseURL, key)
	if c == nil {
		return nil
	}
	return &StoreAPI{c: c}
}

type storeContent struct {
	ContentID   string `json:"content_id"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

type storeVersion struct {
	TitleID      string         `json:"title_id"`
	Version      uint32         `json:"version"`
	VersionLabel string         `json:"version_label"`
	Content      []storeContent `json:"content"`
}

type storeText struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type storeTitle struct {
	TitleID       string               `json:"title_id"`
	Published     bool                 `json:"published"`
	Publisher     string               `json:"publisher"`
	Category      string               `json:"category"`
	Localized     map[string]storeText `json:"localized"`
	DefaultLang   string               `json:"default_lang"`
	Languages     []string             `json:"languages"`
	Compatibility []string             `json:"compatibility"`
	Screenshots   []string             `json:"screenshots"`
	Distribution  struct {
		Source     string `json:"source"`
		Permission string `json:"permission"`
	} `json:"distribution"`
	IconPath     string        `json:"icon_path"`
	SizeBytes    int64         `json:"size_bytes"`
	Version      uint32        `json:"version"`
	VersionLabel string        `json:"version_label"`
	BaseVersion  uint32        `json:"base_version"`
	UpdatedAt    int64         `json:"updated_at"`
	Application  *storeVersion `json:"application"`
	Update       *storeVersion `json:"update"`
}

// Name is the default language's name, else any.
func (t storeTitle) Name() string {
	if l, ok := t.Localized[t.DefaultLang]; ok && l.Name != "" {
		return l.Name
	}
	for _, k := range sortedKeys(t.Localized) {
		if t.Localized[k].Name != "" {
			return t.Localized[k].Name
		}
	}
	return t.TitleID
}

// Updated is updated_at as a time.
func (t storeTitle) Updated() time.Time { return time.Unix(t.UpdatedAt, 0) }

func sortedKeys(m map[string]storeText) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type storeStats struct {
	TitleID         string         `json:"title_id"`
	Downloads       int64          `json:"downloads"`
	ContentRequests int64          `json:"content_requests"`
	BytesServed     int64          `json:"bytes_served"`
	LastDownloadAt  int64          `json:"last_download_at"`
	Entitlements    int64          `json:"entitlements"`
	Tasks           map[string]int `json:"tasks"`
}

// LastDownload is last_download_at as a time; zero when never.
func (s storeStats) LastDownload() time.Time {
	if s.LastDownloadAt == 0 {
		return time.Time{}
	}
	return time.Unix(s.LastDownloadAt, 0)
}

func tid(id string) string { return "/internal/store/titles/" + url.PathEscape(id) }

func (a *StoreAPI) titles(ctx context.Context) ([]storeTitle, error) {
	var out struct {
		Titles []storeTitle `json:"titles"`
	}
	err := a.c.sendJSON(ctx, http.MethodGet, "/internal/store/titles", nil, &out)
	return out.Titles, err
}

func (a *StoreAPI) title(ctx context.Context, id string) (storeTitle, error) {
	var t storeTitle
	err := a.c.sendJSON(ctx, http.MethodGet, tid(id), nil, &t)
	return t, err
}

func (a *StoreAPI) stats(ctx context.Context) (map[string]storeStats, error) {
	var out struct {
		Titles []storeStats `json:"titles"`
	}
	err := a.c.sendJSON(ctx, http.MethodGet, "/internal/store/stats", nil, &out)
	m := map[string]storeStats{}
	for _, s := range out.Titles {
		m[s.TitleID] = s
	}
	return m, err
}

func (a *StoreAPI) titleStats(ctx context.Context, id string) (storeStats, error) {
	var s storeStats
	err := a.c.sendJSON(ctx, http.MethodGet, tid(id)+"/stats", nil, &s)
	return s, err
}

// storeForm is the title editor. One language at a time, as the news form
// does; the others are carried over on save.
type storeForm struct {
	base
	Exists                                bool
	Item                                  storeTitle
	TitleID, Publisher, Category          string
	Lang, Name, Description               string
	Languages, Compatibility, Screenshots string
	Source, Permission                    string
	Stats                                 *storeStats
	Error                                 string
}

func (s *Server) storeOff(w http.ResponseWriter, r *http.Request) bool {
	if s.Store != nil {
		return false
	}
	// The section is hidden when unset; a direct link answers 404 rather than a form.
	http.NotFound(w, r)
	return true
}

func (s *Server) handleStore(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.storeOff(w, r) {
		return
	}
	v := struct {
		base
		Titles []storeTitle
		Stats  map[string]storeStats
		Error  string
	}{base: s.page(r, ws, i18n.T("en", "admin.store"))}
	v.Flash = storeFlash(r.URL.Query().Get("m"))
	var err error
	if v.Titles, err = s.Store.titles(r.Context()); err != nil {
		s.log.Error("store titles", "err", err)
		v.Error = err.Error()
	}
	if v.Stats, err = s.Store.stats(r.Context()); err != nil && v.Error == "" {
		v.Error = err.Error()
	}
	s.render(w, http.StatusOK, "admin_store.html", v)
}

func storeFlash(code string) string {
	switch code {
	case "created", "saved", "icon", "uploaded", "published", "unpublished", "withdrawn", "deleted":
		return i18n.T("en", "admin.store.flash."+code)
	}
	return ""
}

func (s *Server) formFor(r *http.Request, ws store.WebSession, t storeTitle, exists bool) storeForm {
	f := storeForm{base: s.page(r, ws, i18n.T("en", "admin.store.new")), Exists: exists, Item: t, TitleID: t.TitleID,
		Publisher: t.Publisher, Category: t.Category, Lang: t.DefaultLang,
		Languages: strings.Join(t.Languages, ", "), Compatibility: strings.Join(t.Compatibility, ", "),
		Screenshots: strings.Join(t.Screenshots, "\n"), Source: t.Distribution.Source, Permission: t.Distribution.Permission}
	if exists {
		f.base = s.page(r, ws, t.Name())
		f.Path = "/admin/store/" + t.TitleID
	}
	if f.Lang == "" {
		f.Lang = "en-US"
	}
	if l, ok := t.Localized[f.Lang]; ok {
		f.Name, f.Description = l.Name, l.Description
	}
	return f
}

func (s *Server) handleStoreNew(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.storeOff(w, r) {
		return
	}
	s.render(w, http.StatusOK, "admin_store_edit.html", s.formFor(r, ws, storeTitle{DefaultLang: "en-US"}, false))
}

func (s *Server) handleStoreTitle(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.storeOff(w, r) {
		return
	}
	s.renderStoreTitle(w, r, ws, r.PathValue("id"), http.StatusOK, "")
}

func (s *Server) renderStoreTitle(w http.ResponseWriter, r *http.Request, ws store.WebSession, id string, status int, errMsg string) {
	t, err := s.Store.title(r.Context(), id)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	f := s.formFor(r, ws, t, true)
	if lang := r.URL.Query().Get("lang"); lang != "" {
		f.Lang = lang
		f.Name, f.Description = t.Localized[lang].Name, t.Localized[lang].Description
	}
	if st, err := s.Store.titleStats(r.Context(), id); err == nil {
		f.Stats = &st
	}
	f.Flash, f.Error = storeFlash(r.URL.Query().Get("m")), errMsg
	s.render(w, status, "admin_store_edit.html", f)
}

func splitLines(v string) []string {
	var out []string
	for l := range strings.SplitSeq(strings.ReplaceAll(v, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// handleStoreSave creates (no id in the path) or updates a title's metadata.
func (s *Server) handleStoreSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || s.storeOff(w, r) {
		return
	}
	id := r.PathValue("id")
	lang := strings.TrimSpace(r.PostFormValue("lang"))
	if lang == "" {
		lang = "en-US"
	}
	localized := map[string]storeText{}
	if id != "" {
		cur, err := s.Store.title(r.Context(), id)
		if err != nil {
			s.renderStoreTitle(w, r, ws, id, http.StatusBadGateway, err.Error())
			return
		}
		for k, v := range cur.Localized {
			localized[k] = v
		}
	}
	localized[lang] = storeText{Name: strings.TrimSpace(r.PostFormValue("name")), Description: strings.ReplaceAll(r.PostFormValue("description"), "\r\n", "\n")}
	body := map[string]any{
		"publisher": strings.TrimSpace(r.PostFormValue("publisher")), "category": strings.TrimSpace(r.PostFormValue("category")),
		"localized": localized, "default_lang": lang,
		"languages": splitList(r.PostFormValue("languages")), "compatibility": splitList(r.PostFormValue("compatibility")),
		"screenshots": splitLines(r.PostFormValue("screenshots")),
		"distribution": map[string]string{"source": strings.TrimSpace(r.PostFormValue("source")),
			"permission": strings.TrimSpace(r.PostFormValue("permission"))},
	}
	for _, k := range []string{"languages", "compatibility", "screenshots"} {
		if body[k] == nil {
			body[k] = []string{}
		}
	}
	var t storeTitle
	var err error
	action, flash := "store.update", "saved"
	if id == "" {
		id = strings.ToLower(strings.TrimSpace(r.PostFormValue("title_id")))
		body["title_id"] = id
		action, flash = "store.create", "created"
		err = s.Store.c.sendJSON(r.Context(), http.MethodPost, "/internal/store/titles", body, &t)
	} else {
		err = s.Store.c.sendJSON(r.Context(), http.MethodPatch, tid(id), body, &t)
	}
	if err != nil {
		if action == "store.create" {
			f := s.formFor(r, ws, storeTitle{TitleID: id, DefaultLang: lang}, false)
			f.Publisher, f.Category, f.Name, f.Description = r.PostFormValue("publisher"), r.PostFormValue("category"), r.PostFormValue("name"), r.PostFormValue("description")
			f.Languages, f.Compatibility, f.Screenshots = r.PostFormValue("languages"), r.PostFormValue("compatibility"), r.PostFormValue("screenshots")
			f.Source, f.Permission, f.Error = r.PostFormValue("source"), r.PostFormValue("permission"), err.Error()
			s.render(w, http.StatusBadRequest, "admin_store_edit.html", f)
			return
		}
		s.renderStoreTitle(w, r, ws, id, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, id)
	http.Redirect(w, r, "/admin/store/"+url.PathEscape(id)+"?m="+flash, http.StatusSeeOther)
}

// handleStoreAction is publish, unpublish, withdrawing the update, or
// deleting the title (which needs the confirm tick).
func (s *Server) handleStoreAction(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || s.storeOff(w, r) {
		return
	}
	id := r.PathValue("id")
	var method, path, flash string
	back := "/admin/store/" + url.PathEscape(id)
	switch r.PathValue("action") {
	case "publish", "unpublish":
		method, path, flash = http.MethodPost, tid(id)+"/"+r.PathValue("action"), r.PathValue("action")+"ed"
	case "withdraw-update":
		method, path, flash = http.MethodDelete, tid(id)+"/versions/update", "withdrawn"
	case "delete":
		if r.PostFormValue("confirm") != "1" {
			s.renderStoreTitle(w, r, ws, id, http.StatusBadRequest, i18n.T("en", "admin.store.confirm"))
			return
		}
		method, path, flash, back = http.MethodDelete, tid(id), "deleted", "/admin/store"
	default:
		http.NotFound(w, r)
		return
	}
	if err := s.Store.c.send(r.Context(), method, path, "", nil, nil); err != nil {
		s.renderStoreTitle(w, r, ws, id, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "store."+r.PathValue("action"), id)
	http.Redirect(w, r, back+"?m="+flash, http.StatusSeeOther)
}

// handleStoreIcon relays an icon (PNG or JPEG, at most 1 MiB) as the raw body nx-baas takes.
func (s *Server) handleStoreIcon(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	ws, ok := s.authorizePost(w, r)
	if !ok || s.storeOff(w, r) {
		return
	}
	id := r.PathValue("id")
	f, h, err := r.FormFile("icon")
	if err != nil {
		s.renderStoreTitle(w, r, ws, id, http.StatusBadRequest, i18n.T("en", "admin.store.noicon"))
		return
	}
	defer f.Close()
	ct := h.Header.Get("Content-Type")
	if ct != "image/png" && ct != "image/jpeg" {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		ct = http.DetectContentType(head[:n])
		_, _ = f.Seek(0, io.SeekStart)
	}
	if err := s.Store.c.send(r.Context(), http.MethodPut, tid(id)+"/icon", ct, f, nil); err != nil {
		s.renderStoreTitle(w, r, ws, id, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "store.icon", id)
	http.Redirect(w, r, "/admin/store/"+url.PathEscape(id)+"?m=icon", http.StatusSeeOther)
}

var (
	contentIDRE = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	sha256RE    = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// parseContents reads the content list an uploader types: a JSON array as
// the API takes it, or one line per NCA, "content_id type [size] [sha256]".
func parseContents(v string) ([]storeContent, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		var out []storeContent
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, fmt.Errorf("contents: %v", err)
		}
		return out, nil
	}
	var out []storeContent
	for i, line := range splitLines(v) {
		f := strings.Fields(strings.ReplaceAll(line, ",", " "))
		if len(f) < 2 || !contentIDRE.MatchString(f[0]) {
			return nil, fmt.Errorf("contents line %d: want \"content_id type [size] [sha256]\"", i+1)
		}
		c := storeContent{ContentID: strings.ToLower(f[0]), ContentType: strings.ToLower(f[1])}
		for _, x := range f[2:] {
			switch {
			case sha256RE.MatchString(x):
				c.SHA256 = strings.ToLower(x)
			default:
				n, err := strconv.ParseInt(x, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("contents line %d: %q is neither a size nor a sha256", i+1, x)
				}
				c.Size = n
			}
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("contents: list every NCA in the package")
	}
	return out, nil
}

// handleStoreUpload streams a version upload to nx-baas. The browser's form
// is read part by part (csrf, kind, version, version_label, contents, then
// the file, in that order); the metadata part is written upstream first and
// the file is copied straight through, so the website never holds the NSP.
func (s *Server) handleStoreUpload(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.storeOff(w, r) {
		return
	}
	id := r.PathValue("id")
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "send the upload form (multipart/form-data)", http.StatusBadRequest)
		return
	}
	fields := map[string]string{}
	var nsp *multipart.Part
	for nsp == nil {
		part, err := mr.NextPart()
		if err != nil {
			s.renderStoreTitle(w, r, ws, id, http.StatusBadRequest, i18n.T("en", "admin.store.nofile"))
			return
		}
		if part.FormName() == "nsp" {
			nsp = part
			break
		}
		b, err := io.ReadAll(io.LimitReader(part, 256<<10))
		if err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		fields[part.FormName()] = string(b)
	}
	// The token must come before the file, so a forged upload is refused
	// before a byte of it is relayed.
	if !s.Sessions.CheckCSRF(ws.CoreSessionID, fields["csrf"]) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	contents, err := parseContents(fields["contents"])
	if err != nil {
		s.renderStoreTitle(w, r, ws, id, http.StatusBadRequest, err.Error())
		return
	}
	version, err := strconv.ParseUint(strings.TrimSpace(fields["version"]), 10, 32)
	if err != nil {
		s.renderStoreTitle(w, r, ws, id, http.StatusBadRequest, "version: a whole number (the content meta version, 0 for a base)")
		return
	}
	kind := fields["kind"]
	if kind != "base" && kind != "update" {
		s.renderStoreTitle(w, r, ws, id, http.StatusBadRequest, "kind: base or update")
		return
	}
	meta, _ := json.Marshal(map[string]any{"kind": kind, "version": version,
		"version_label": strings.TrimSpace(fields["version_label"]), "contents": contents})

	pr, pw := io.Pipe()
	up := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			mw, err := up.CreateFormField("metadata")
			if err != nil {
				return err
			}
			if _, err := mw.Write(meta); err != nil {
				return err
			}
			fw, err := up.CreateFormFile("nsp", nsp.FileName())
			if err != nil {
				return err
			}
			if _, err := io.Copy(fw, nsp); err != nil {
				return err
			}
			return up.Close()
		}()
		pw.CloseWithError(err)
	}()
	var t storeTitle
	err = s.Store.c.send(r.Context(), http.MethodPost, tid(id)+"/versions", up.FormDataContentType(), pr, &t)
	pr.Close()
	if err != nil {
		s.log.Info("store upload refused", "title", id, "err", err)
		s.renderStoreTitle(w, r, ws, id, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "store.upload", fmt.Sprintf("%s kind=%s version=%d label=%q", id, kind, version, fields["version_label"]))
	http.Redirect(w, r, "/admin/store/"+url.PathEscape(id)+"?m=uploaded", http.StatusSeeOther)
}
