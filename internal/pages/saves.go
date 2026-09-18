package pages

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/saves"
)

// savesView is the cloud saves page: every platform's saves under the account, the
// allowance, and the player's own storage.
type savesView struct {
	baseData
	Available   bool
	Detail      string // the service's own words on a failure, under the flash
	Saves       []saveRow
	Consoles    []string // the consoles the saves span, for the filter
	Usage       saves.Usage
	UsedText    string
	LimitText   string
	Percent     int
	Storage     saves.Storage
	TestState   string // ok | bad | idle: the tint of the storage row
	Presets     []preset
	Kinds       map[string]bool // which kinds this deployment offers
	StorageName string          // one line naming the connected storage
}

type saveRow struct {
	Platform string
	Console  string // the console the platform emulates, for the logo
	Name     string // the game's name when known, else the id
	Icon     bool   // a Switch title: /titles/icon/{id} draws its icon
	Conflict bool   // some version conflicts
	TitleID  string
	Versions []versionRow
}

type versionRow struct {
	saves.Version
	SizeText string
	When     string
	Single   bool // one part: downloadable here
}

// preset is a provider hint for the storage form; the values are documented by each provider.
type preset struct{ Name, Endpoint, Region, Hint string }

var presets = []preset{
	{"Cloudflare R2", "<account id>.r2.cloudflarestorage.com", "auto", "10 GB free; path-style"},
	{"Backblaze B2", "s3.<region>.backblazeb2.com", "<region>, e.g. us-west-004", "10 GB free; path-style"},
	{"Amazon S3", "s3.<region>.amazonaws.com", "<region>, e.g. eu-west-1", ""},
	{"MinIO or Garage", "<host>:<port>", "us-east-1", "tick plain HTTP for a LAN box without TLS"},
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (s *Server) handleSaves(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	ctx := r.Context()
	view := savesView{baseData: s.data(r, v, i18n.T("en", "saves.title")), Detail: r.URL.Query().Get("d"), Presets: presets}
	list, usage, err := s.Saves.List(ctx, v.AccountID)
	if err != nil {
		if !errors.Is(err, saves.ErrUnavailable) {
			s.log.Error("saves list", "err", err)
		}
		s.render(w, http.StatusOK, "saves.html", view)
		return
	}
	view.Available = true
	view.Usage = usage
	view.UsedText, view.LimitText = sizeText(usage.AllowanceUsed), sizeText(usage.Allowance)
	if usage.Allowance > 0 {
		view.Percent = int(usage.AllowanceUsed * 100 / usage.Allowance)
	}
	for _, sv := range list {
		row := saveRow{Platform: sv.Platform, Console: platformConsole(sv.Platform), Name: titleName(sv.TitleID), TitleID: sv.TitleID}
		if row.Console == "Switch" && switchID.MatchString(sv.TitleID) {
			row.Icon = true
			if n := switchTitleName(ctx, sv.TitleID); n != "" {
				row.Name = n
			}
		}
		for _, ver := range sv.Versions {
			when := ver.CreatedAt
			if ver.SavedAt != nil && !ver.SavedAt.IsZero() && ver.SavedAt.Unix() > 0 {
				when = *ver.SavedAt
			}
			row.Versions = append(row.Versions, versionRow{Version: ver, SizeText: sizeText(ver.Size), When: when.UTC().Format("2006-01-02 15:04"), Single: ver.Parts == 1})
			row.Conflict = row.Conflict || ver.Conflict
		}
		if len(row.Versions) == 0 {
			continue // nothing to pick in the dropdown
		}
		view.Saves = append(view.Saves, row)
		if !slices.Contains(view.Consoles, row.Console) {
			view.Consoles = append(view.Consoles, row.Console)
		}
	}
	view.Kinds, _ = s.Saves.Kinds(ctx)
	if st, err := s.Saves.Storage(ctx, v.AccountID); err == nil {
		view.Storage = st
		view.StorageName = storageName(st)
		view.TestState = "idle"
		if st.LastTestOK != nil {
			view.TestState = "bad"
			if *st.LastTestOK {
				view.TestState = "ok"
			}
		}
	} else {
		s.log.Error("saves storage", "err", err)
	}
	s.render(w, http.StatusOK, "saves.html", view)
}

// platformConsole names the console behind a saves platform: the emulators' own names and the
// console's. Unknown platforms fall through to consoleSlug's generic logo.
func platformConsole(p string) string {
	switch strings.ToLower(p) {
	case "citron", "switch":
		return "Switch"
	case "wii":
		return "Wii"
	case "ds":
		return "DS"
	}
	return p
}

// back sends the player to the saves page with a flash and, when there is one, the service's
// own explanation.
func back(w http.ResponseWriter, r *http.Request, msg string, err error) {
	to := "/account/saves?m=" + url.QueryEscape(msg)
	if err != nil {
		to += "&d=" + url.QueryEscape(err.Error())
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) handleSavesStorage(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	var cfg saves.StorageConfig
	switch r.PostFormValue("kind") {
	case "webdav":
		cfg = saves.StorageConfig{Kind: "webdav", WebDAV: &saves.WebDAVConfig{
			URL:      strings.TrimSpace(r.PostFormValue("url")),
			Username: strings.TrimSpace(r.PostFormValue("username")),
			Password: r.PostFormValue("password"),
		}}
	default:
		cfg = saves.StorageConfig{Kind: "s3", S3: &saves.S3Config{
			Endpoint:  strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(r.PostFormValue("endpoint"), "https://"), "http://")),
			Region:    strings.TrimSpace(r.PostFormValue("region")),
			Bucket:    strings.TrimSpace(r.PostFormValue("bucket")),
			Prefix:    strings.Trim(strings.TrimSpace(r.PostFormValue("prefix")), "/"),
			AccessKey: strings.TrimSpace(r.PostFormValue("access_key")),
			SecretKey: strings.TrimSpace(r.PostFormValue("secret_key")),
			Insecure:  r.PostFormValue("insecure") == "on",
			PathStyle: r.PostFormValue("path_style") == "on",
		}}
	}
	warning, err := s.Saves.Connect(r.Context(), v.AccountID, cfg)
	if err != nil {
		back(w, r, "storage-failed", err)
		return
	}
	if warning != "" {
		back(w, r, "storage-warning", errors.New(warning))
		return
	}
	back(w, r, "storage-saved", nil)
}

func (s *Server) handleSavesDisconnect(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if err := s.Saves.Disconnect(r.Context(), v.AccountID); err != nil {
		back(w, r, "storage-failed", err)
		return
	}
	back(w, r, "storage-disconnected", nil)
}

func (s *Server) handleSavesMove(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if err := s.Saves.Move(r.Context(), v.AccountID); err != nil {
		back(w, r, "storage-failed", err)
		return
	}
	back(w, r, "storage-moving", nil)
}

func versionID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PostFormValue("version"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) handleSavesRestore(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	id, ok := versionID(r)
	if !ok {
		back(w, r, "saves-failed", nil)
		return
	}
	if err := s.Saves.Restore(r.Context(), v.AccountID, id); err != nil {
		back(w, r, "saves-failed", err)
		return
	}
	back(w, r, "saves-restored", nil)
}

func (s *Server) handleSavesDelete(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	id, ok := versionID(r)
	if !ok {
		back(w, r, "saves-failed", nil)
		return
	}
	if err := s.Saves.Delete(r.Context(), v.AccountID, id); err != nil {
		back(w, r, "saves-failed", err)
		return
	}
	back(w, r, "saves-deleted", nil)
}

func (s *Server) handleSavesDownload(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.sendSave(w, r, v.AccountID, id)
}

// handleSavesDownloadPost is the game row's download button: the version comes from the
// row's dropdown, so it rides in the same form as pin, restore and delete.
func (s *Server) handleSavesDownloadPost(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	id, ok := versionID(r)
	if !ok {
		back(w, r, "saves-failed", nil)
		return
	}
	s.sendSave(w, r, v.AccountID, id)
}

func (s *Server) sendSave(w http.ResponseWriter, r *http.Request, accountID string, id int64) {
	resp, err := s.Saves.Download(r.Context(), accountID, id)
	if err != nil {
		back(w, r, "saves-failed", err)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"save-%d.bin\"", id))
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	_, _ = io.Copy(w, resp.Body)
}

// storageName is the one line the page shows for a connected storage, whatever its kind.
func storageName(st saves.Storage) string {
	switch st.Kind {
	case "webdav":
		return st.URL
	case "gdrive":
		if st.Account != "" {
			return "Google Drive · " + st.Account
		}
		return "Google Drive"
	case "onedrive":
		if st.Account != "" {
			return "OneDrive · " + st.Account
		}
		return "OneDrive"
	}
	return st.Bucket + " · " + st.Endpoint
}

// oauthState binds a consent round trip to the account without a database row: the player's
// account id plus an HMAC the callback recomputes. The website session secret signs it, so a
// state forged elsewhere never verifies.
func (s *Server) oauthState(accountID string) string {
	return accountID + "." + s.Sessions.HashToken("oauth:"+accountID)
}

func (s *Server) accountFromState(state string) (string, bool) {
	i := strings.LastIndexByte(state, '.')
	if i <= 0 {
		return "", false
	}
	acct := state[:i]
	if s.Sessions.HashToken("oauth:"+acct) != state[i+1:] {
		return "", false
	}
	return acct, true
}

func (s *Server) oauthRedirect(r *http.Request, provider string) string {
	scheme := "https"
	if !s.Production {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/account/saves/storage/oauth/" + provider + "/callback"
}

// handleSavesOAuthStart sends the signed-in player to the provider's consent page.
func (s *Server) handleSavesOAuthStart(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	provider := r.PathValue("provider")
	u, err := s.Saves.OAuthURL(r.Context(), provider, s.oauthRedirect(r, provider), s.oauthState(v.AccountID))
	if err != nil {
		back(w, r, "storage-failed", err)
		return
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

// handleSavesOAuthCallback is where the provider sends the player back. The state names the
// account, so this works for a consent started from the app as well as from this site.
func (s *Server) handleSavesOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	acct, ok := s.accountFromState(r.URL.Query().Get("state"))
	if !ok {
		back(w, r, "storage-failed", errors.New("the sign-in did not come back the way it left"))
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		back(w, r, "storage-failed", errors.New(e))
		return
	}
	warning, err := s.Saves.OAuthConnect(r.Context(), acct, provider, r.URL.Query().Get("code"), s.oauthRedirect(r, provider))
	if err != nil {
		back(w, r, "storage-failed", err)
		return
	}
	if warning != "" {
		back(w, r, "storage-warning", errors.New(warning))
		return
	}
	back(w, r, "storage-saved", nil)
}
