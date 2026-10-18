// Package pages serves the server-rendered website (landing, account
// portal, friends, status). No JavaScript is required for any journey
// (WEB-10): every action is an HTML form.
package pages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/news"
	"github.com/openpak/website/internal/pctl"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
	"github.com/openpak/website/internal/titleart"
	"github.com/openpak/website/internal/wfc"
)

// Server wires the page handlers.
type Server struct {
	Core     core.HTTP
	Identity core.Identity
	Social   core.Social
	// Moderation finds an account by exact email for "add friend by email".
	Moderation core.Moderation
	Links      core.Links
	Events     core.Events
	Invites    core.InvitationsView
	// Playtime marks titles tested from online play; nil leaves the titles page on player reports.
	Playtime core.Playtime
	DB       store.Store
	Media    *media.Service
	// Adapter answers what only the Switch adapter knows: the friend code.
	Adapter *adapter.Client
	// WFC resolves Wii/DS friend codes for the link flow; nil hides the form.
	WFC *wfc.Client
	// Saves is the cloud saves service; nil renders the page as "not available".
	Saves *saves.Client
	// Parental relays to nx-baas's parental-controls records; nil renders the
	// page as "not available".
	Parental *pctl.Client
	// Chat is the neutral chat service; nil renders the page as "not available".
	Chat *chat.Client
	// Support is the help-ticket service; nil says tickets are not available.
	Support *support.Service
	// News is the public news feed for the landing page; nil shows none.
	News       *news.Feed
	Sessions   *session.Manager
	Production bool
	// AdminAccountIDs mirrors the admin allowlist so the portal can show
	// the moderation section to the accounts that can actually open it.
	AdminAccountIDs []string
	AuthLimit       *ratelimit.Limiter
	MediaLimit      *ratelimit.Limiter
	tmpl            *template.Template
	log             *slog.Logger
}

// New compiles templates and returns the server.
func New(tpl *template.Template, log *slog.Logger) *Server {
	return &Server{tmpl: tpl, log: log,
		Adapter: adapter.New(os.Getenv("ADAPTER_INTERNAL_URL"), os.Getenv("NX_INTERNAL_KEY")),
		WFC:     wfc.New(os.Getenv("WFC_API_URL"), os.Getenv("WFC_API_SECRET"))}
}

// viewer is the signed-in identity for one request.
type viewer struct {
	CoreSessionID string
	store.WebSession
}

// baseData feeds the layout template.
type baseData struct {
	// Asset is a fingerprint of the static files, appended to their URLs. Without it a CDN
	// keeps serving the previous deploy's stylesheet for as long as its TTL says it may —
	// which is exactly what happened: a cached 2.6 KB site.css against a 16 KB one on disk.
	Asset   string
	Lang    string
	Title   string
	Banner  string
	RegOpen bool
	User    *viewUser
	Flash   string
	CSRF    string
	Path    string
	Admin   bool
}

type viewUser struct {
	ID          string
	Email       string
	DisplayName string
}

// data builds baseData for a request.
func (s *Server) data(r *http.Request, v *viewer, title string) baseData {
	d := baseData{Asset: assetVersion(), Lang: i18n.FromRequest(r), Title: title, Path: r.URL.Path}
	if cfg, err := s.DB.GetSiteConfig(r.Context()); err == nil {
		d.Banner = cfg.BannerMessage
		d.RegOpen = cfg.RegistrationsOpen
	}
	if v != nil {
		d.User = &viewUser{ID: v.AccountID, Email: v.Email, DisplayName: v.DisplayName}
		d.Admin = slices.Contains(s.AdminAccountIDs, v.AccountID)
		d.CSRF = s.Sessions.CSRFToken(v.CoreSessionID)
	}
	d.Flash = flashText(d.Lang, r.URL.Query().Get("m"))
	return d
}

// flashText maps short redirect codes to catalogue strings.
func flashText(lang, code string) string {
	switch code {
	case "saved":
		return i18n.T(lang, "msg.saved")
	case "pwreset":
		return i18n.T(lang, "auth.reset.ok")
	case "frequest":
		return i18n.T(lang, "friends.request.ok")
	case "frequest-email":
		return i18n.T(lang, "friends.request.email")
	case "funknown":
		return i18n.T(lang, "friends.request.unknown")
	case "fself":
		return i18n.T(lang, "friends.request.self")
	case "wfc-linked":
		return i18n.T(lang, "account.links.wfc.ok")
	case "wfc-notfound":
		return i18n.T(lang, "account.links.wfc.notfound")
	case "wfc-taken":
		return i18n.T(lang, "account.links.wfc.taken")
	case "password-changed":
		return i18n.T(lang, "account.password.ok")
	case "password-mismatch":
		return i18n.T(lang, "account.password.mismatch")
	case "password-failed":
		return i18n.T(lang, "account.password.failed")
	case "profile-saved":
		return i18n.T(lang, "account.profile.ok")
	case "profile-failed":
		return i18n.T(lang, "account.profile.failed")
	case "delete-unconfirmed":
		return i18n.T(lang, "account.delete.unconfirmed")
	case "email-sent":
		return i18n.T(lang, "account.email.sent")
	case "email-wrongpassword":
		return i18n.T(lang, "account.email.wrongpassword")
	case "email-invalid":
		return i18n.T(lang, "account.email.invalid")
	case "verify-sent":
		return i18n.T(lang, "account.verify.sent")
	case "2fa-disabled":
		return i18n.T(lang, "account.2fa.disabled")
	case "2fa-wrongpassword":
		return i18n.T(lang, "account.2fa.wrongpassword")
	case "2fa-wrongcode":
		return i18n.T(lang, "account.2fa.wrongcode")
	case "2fa-locked":
		return i18n.T(lang, "auth.2fa.locked")
	case "2fa-state":
		return i18n.T(lang, "account.2fa.state")
	case "chat-blocked":
		return i18n.T(lang, "chat.blocked")
	case "chat-notfriends":
		return i18n.T(lang, "chat.notfriends")
	case "storage-saved":
		return i18n.T(lang, "saves.storage.ok")
	case "storage-warning":
		return i18n.T(lang, "saves.storage.warning")
	case "storage-failed":
		return i18n.T(lang, "saves.storage.failed")
	case "storage-disconnected":
		return i18n.T(lang, "saves.storage.disconnected")
	case "storage-moving":
		return i18n.T(lang, "saves.storage.moving")
	case "saves-saved":
		return i18n.T(lang, "msg.saved")
	case "saves-restored":
		return i18n.T(lang, "saves.restored")
	case "saves-deleted":
		return i18n.T(lang, "saves.deleted")
	case "saves-deleted-all":
		return i18n.T(lang, "saves.deleted.all")
	case "ticket-opened":
		return i18n.T(lang, "help.opened")
	case "ticket-replied":
		return i18n.T(lang, "help.replied")
	case "saves-failed":
		return i18n.T(lang, "saves.failed")
	default:
		return ""
	}
}

// render executes a template with the layout.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; style-src 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// viewerOf resolves the signed-in user or nil. A dead core session clears
// the cookie and the index row so state never lies.
func (s *Server) viewerOf(w http.ResponseWriter, r *http.Request) *viewer {
	id := s.Sessions.CoreSessionID(r)
	if id == "" {
		return nil
	}
	ws, err := s.DB.GetWebSession(r.Context(), id)
	if err != nil {
		s.Sessions.ClearCookie(w)
		return nil
	}
	// Validate against the core: a revoked session (logout elsewhere, ban,
	// password reset) ends the website session too.
	if _, err := s.Core.ListSessions(r.Context(), id); err != nil {
		_ = s.DB.DeleteWebSession(r.Context(), id)
		s.Sessions.ClearCookie(w)
		return nil
	}
	return &viewer{CoreSessionID: id, WebSession: ws}
}

// requireViewer redirects signed-out users to the login page.
func (s *Server) requireViewer(w http.ResponseWriter, r *http.Request) *viewer {
	v := s.viewerOf(w, r)
	if v == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
	return v
}

// postGuard enforces method + CSRF for state-changing forms. Multipart
// bodies (avatar upload) are parsed too so the csrf field is visible.
func (s *Server) postGuard(w http.ResponseWriter, r *http.Request) (*viewer, bool) {
	v := s.requireViewer(w, r)
	if v == nil {
		return nil, false
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return nil, false
		}
	} else if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return nil, false
	}
	if !s.Sessions.CheckCSRF(v.CoreSessionID, r.PostFormValue("csrf")) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return nil, false
	}
	return v, true
}

func redirectMsg(w http.ResponseWriter, r *http.Request, to, msg string) {
	http.Redirect(w, r, to+"?m="+url.QueryEscape(msg), http.StatusSeeOther)
}

// Handler returns a standalone mux with all page routes; used by tests and
// usable for embedding the page surface in another server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return mux
}

// RegisterRoutes wires the public page routes.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.handleLanding)
	mux.HandleFunc("GET /titles", s.handleTitles)
	mux.HandleFunc("POST /request/vote", s.handleVote)
	mux.HandleFunc("POST /admin/titles/status", s.handleSetTitleStatus)
	mux.HandleFunc("POST /admin/titles/shops", s.handleSetShopLinks)
	mux.HandleFunc("POST /admin/titles/playability", s.handleSetPlayability)
	mux.HandleFunc("GET /register", s.handleRegisterForm)
	mux.HandleFunc("POST /register", s.handleRegisterSubmit)
	mux.HandleFunc("GET /verify", s.handleVerify)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /login/2fa", s.handleLogin2FASubmit)
	mux.HandleFunc("GET /email/confirm", s.handleEmailConfirmForm)
	mux.HandleFunc("POST /email/confirm", s.handleEmailConfirmSubmit)
	mux.HandleFunc("POST /account/email", s.handleEmailChange)
	mux.HandleFunc("POST /account/verify/resend", s.handleVerifyResend)
	mux.HandleFunc("POST /account/2fa/setup", s.handle2FASetup)
	mux.HandleFunc("POST /account/2fa/enable", s.handle2FAEnable)
	mux.HandleFunc("POST /account/2fa/disable", s.handle2FADisable)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /forgot-password", s.handleForgotForm)
	mux.HandleFunc("POST /forgot-password", s.handleForgotSubmit)
	mux.HandleFunc("GET /reset-password", s.handleResetForm)
	mux.HandleFunc("POST /reset-password", s.handleResetSubmit)
	mux.HandleFunc("GET /account", s.handleAccount)
	mux.HandleFunc("POST /account/password", s.handleChangePassword)
	mux.HandleFunc("POST /account/profile", s.handleUpdateProfile)
	mux.HandleFunc("POST /account/delete", s.handleDeleteAccount)
	mux.HandleFunc("POST /account/sessions/revoke", s.handleRevokeSession)
	mux.HandleFunc("POST /account/link/revoke", s.handleUnlink)
	mux.HandleFunc("POST /account/link/wfc", s.handleLinkWFC)
	mux.HandleFunc("POST /account/networks/{network}/{field}", s.handleNetworkField)
	mux.HandleFunc("POST /account/avatar", s.handleAvatarUpload)
	mux.HandleFunc("GET /media/avatars/{accountID}/{size}", s.handleAvatarServe)
	mux.HandleFunc("GET /account/friends", s.handleFriends)
	mux.HandleFunc("POST /account/friends/request", s.handleFriendRequest)
	mux.HandleFunc("POST /account/friends/accept", s.handleFriendAccept)
	mux.HandleFunc("POST /account/friends/decline", s.handleFriendDecline)
	mux.HandleFunc("POST /account/friends/remove", s.handleFriendRemove)
	mux.HandleFunc("POST /account/friends/block", s.handleFriendBlock)
	mux.HandleFunc("POST /account/friends/unblock", s.handleFriendUnblock)
	mux.HandleFunc("GET /account/invitations", s.handleInvitations)
	mux.HandleFunc("POST /account/invitations/decline", s.handleDeclineInvitation)
	mux.HandleFunc("GET /account/chat", s.handleChat)
	mux.HandleFunc("POST /account/chat/send", s.handleChatSend)
	mux.HandleFunc("GET /account/saves", s.handleSaves)
	mux.HandleFunc("GET /account/parental", s.handleParental)
	mux.HandleFunc("POST /account/parental/pair", s.handleParentalPair)
	mux.HandleFunc("POST /account/parental/pin", s.handleParentalPin)
	mux.HandleFunc("POST /account/parental/pin/set", s.handleParentalSetPin)
	mux.HandleFunc("POST /account/parental/setting", s.handleParentalSetting)
	mux.HandleFunc("POST /account/parental/remove", s.handleParentalRemove)
	mux.HandleFunc("POST /account/parental/name", s.handleParentalName)
	mux.HandleFunc("GET /account/parental/{moonID}/players/{playerID}/image", s.handleParentalPlayerImage)
	mux.HandleFunc("GET /titles/icon/{id}", s.handleSwitchIcon)
	mux.HandleFunc("POST /account/saves/storage", s.handleSavesStorage)
	mux.HandleFunc("POST /account/saves/storage/disconnect", s.handleSavesDisconnect)
	mux.HandleFunc("POST /account/saves/storage/move", s.handleSavesMove)
	mux.HandleFunc("GET /account/saves/storage/oauth/{provider}/start", s.handleSavesOAuthStart)
	mux.HandleFunc("GET /account/saves/storage/oauth/{provider}/callback", s.handleSavesOAuthCallback)
	mux.HandleFunc("POST /account/saves/restore", s.handleSavesRestore)
	mux.HandleFunc("GET /account/saves/delete", s.handleSavesConfirm)
	mux.HandleFunc("POST /account/saves/delete", s.handleSavesDelete)
	mux.HandleFunc("POST /account/saves/delete-title", s.handleSavesDeleteTitle)
	mux.HandleFunc("GET /account/saves/manage", s.handleSavesManage)
	mux.HandleFunc("GET /account/saves/download/{id}", s.handleSavesDownload)
	mux.HandleFunc("POST /account/saves/download", s.handleSavesDownloadPost)
	mux.HandleFunc("GET /features", s.handleFeatures)
	mux.HandleFunc("GET /downloads", s.handleDownloads)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /lang", s.handleLang)
	mux.HandleFunc("GET /feed/news.xml", s.handleNewsFeed)
	mux.HandleFunc("GET /feed/titles.xml", s.handleTitlesFeed)
	mux.HandleFunc("GET /help", s.handleHelp)
	mux.Handle("GET /support", http.RedirectHandler("/help", http.StatusMovedPermanently))
	mux.HandleFunc("POST /help", s.handleHelpOpen)
	mux.HandleFunc("GET /help/{id}", s.handleHelpTicket)
	mux.HandleFunc("POST /help/{id}", s.handleHelpReply)
	mux.HandleFunc("GET /help/attachments/{msg}", s.handleHelpAttachment)
	// Console linking: the page itself lives in the Switch adapter, but a phone must reach it
	// under a name with a certificate it trusts — a QR pointing at a bare IP means a warning
	// page, and one pointing at accounts.nintendo.com only resolves on the console.
	if adapter := os.Getenv("ADAPTER_LINK_URL"); adapter != "" {
		mux.Handle("/link", adapterProxy(adapter))
		mux.Handle("/link/", adapterProxy(adapter))
	}
	// The mod catalogue is its own service; the emulator and the app reach it under openpak.org
	// with the same bearer they already hold (mods asks /api/v1/me who it belongs to).
	if mods := os.Getenv("MODS_URL"); mods != "" {
		for _, prefix := range []string{"/api/v1/titles/", "/api/v1/mods/", "/api/v1/submissions", "/api/v1/me/submissions", "/api/v1/me/favourites", "/api/v1/me/favourites/"} {
			mux.Handle(prefix, adapterProxy(mods))
		}
	}
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "page.home"))
	titles, err := s.DB.ListRequests(r.Context(), "title", "")
	if err != nil {
		s.log.Error("list requests", "err", err)
	}
	// The HOME shelf: what is being played right now first, then the featured
	// few, as tiles. Players 0 is a featured title nobody is in at the moment.
	type tile struct {
		Name, Console, Href string
		Art                 string // the tile's art: a Switch icon or a cover tile; "" draws the console mark
		Players             int
	}
	type landing struct {
		baseData
		Shelf       []tile
		TitlesTotal int
		News        []news.Article
		Platforms   []platform
		// The live readout: who is on right now. CountsOK false hides it rather
		// than showing zeros that would read as an empty network.
		CountsOK       bool
		Online, InGame int
	}
	// News is a nice-to-have on this page: unset or failing, the section is
	// simply not drawn.
	view := landing{baseData: d, TitlesTotal: len(titles), News: s.News.Latest(r.Context(), 3), Platforms: platforms[:8]}
	byID := map[string]store.Request{}
	for _, t := range titles {
		if t.Name == "" {
			continue
		}
		for _, id := range append([]string{t.TitleID}, t.TitleIDs...) {
			if id != "" {
				byID[strings.ToUpper(id)] = t
			}
		}
	}
	href := func(t store.Request) string { return "/titles#t-" + strconv.FormatInt(t.ID, 10) }
	const shelfMax = 5
	seen := map[string]bool{}
	if s.Invites != nil {
		if counts, _, total, err := s.Invites.PlayerCounts(r.Context()); err == nil {
			view.CountsOK, view.Online = true, total
			sort.Slice(counts, func(i, j int) bool { return counts[i].Players > counts[j].Players })
			for _, c := range counts {
				t, ok := byID[strings.ToUpper(c.TitleID)]
				if !ok {
					t, ok = byID[baseTitleID(c.TitleID)]
				}
				if !ok {
					continue
				}
				view.InGame += c.Players
				if len(view.Shelf) == shelfMax {
					continue
				}
				tl := tile{Name: t.Name, Console: namespaceConsole(c.Namespace), Href: href(t), Players: c.Players}
				if c.Namespace == "switch" && switchID.MatchString(c.TitleID) {
					tl.Art = "/titles/icon/" + c.TitleID
				}
				seen[t.Name] = true
				if tl.Art == "" {
					tl.Art = artPath(t.Console, t.Name)
				}
				view.Shelf = append(view.Shelf, tl)
			}
		} else {
			s.log.Error("player counts", "err", err)
		}
	}
	for _, t := range popularTitles(titles) {
		if len(view.Shelf) == shelfMax || seen[t.Name] {
			continue
		}
		tl := tile{Name: t.Name, Console: t.Console, Href: href(t), Art: artPath(t.Console, t.Name)}
		if t.Console == "Switch" && t.TitleID != "" {
			tl.Art = "/titles/icon/" + t.TitleID
		}
		view.Shelf = append(view.Shelf, tl)
	}
	s.render(w, http.StatusOK, "landing.html", view)
}

// handleFeatures is the honest feature matrix: every network feature and its
// real status (working/broken/partial/planned/…), from the requests table.
// Display-only — voting is for games, so there are no vote controls here.
func (s *Server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "features.title"))
	reqs, err := s.DB.ListRequests(r.Context(), "feature", "")
	if err != nil {
		s.log.Error("list requests", "err", err)
	}
	// Distinct statuses for the filter dropdown, in stable order.
	seen := map[string]bool{}
	var statuses, consoles []string
	for _, f := range reqs {
		if f.Status != "" && !seen[f.Status] {
			seen[f.Status] = true
			statuses = append(statuses, f.Status)
		}
		if f.Console != "" && !seen["c:"+f.Console] {
			seen["c:"+f.Console] = true
			consoles = append(consoles, f.Console)
		}
	}
	sort.Strings(statuses)
	type featuresPage struct {
		baseData
		Features []store.Request
		Statuses []string
		Consoles []string
	}
	s.render(w, http.StatusOK, "features.html", featuresPage{d, reqs, statuses, consoles})
}

// handleVote toggles the viewer's vote on one request and returns them to the
// page they voted from. Session-gated and CSRF-checked by postGuard.
func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	back := safeBack(r.PostFormValue("back"))
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	// A vote on a non-votable id (live/beta game, or a feature) is silently
	// dropped: the UI never offers the button, so this only fires on a stale
	// page or a crafted request, and it is not an error worth logging.
	if _, err := s.DB.ToggleVote(r.Context(), id, v.AccountID); err != nil &&
		!errors.Is(err, store.ErrNotVotable) && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("toggle vote", "err", err)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// safeBack allows only the two pages that carry vote buttons, optionally in the
// scoreboard view — never an arbitrary URL, so the form cannot open-redirect.
func safeBack(back string) string {
	switch back {
	case "/titles", "/titles?sort=votes", "/features", "/features?sort=votes":
		return back
	default:
		return "/titles"
	}
}

// download is one client on the downloads page. URL empty means it is not public yet;
// Icon, when set, stands in for the console's mark.
type download struct {
	Name, Console, Kind, Status, URL, Icon string
}

// Only released public downloads are linked; private repositories and unpublished
// builds remain visible without sending visitors to a missing release page.
var emulators = []download{
	{"Harbor", "Switch", "downloads.kind.emulator", "", "", "/static/clients/harbor.svg"},
	{"Quay", "Wii U", "downloads.kind.emulator", "", "", "/static/clients/quay.svg"},
	{"Azahar", "3DS", "downloads.kind.emulator", "", "", "/static/clients/azahar.png"},
	{"Marina", "Wii", "downloads.kind.emulator", "", "", "/static/clients/marina.svg"},
	{"melonDS", "DS", "downloads.kind.emulator", "", "", "/static/clients/melonds.png"},
	{"Berth", "Xbox", "downloads.kind.emulator", "", "", "/static/clients/berth.svg"},
	{"Wharf", "Xbox 360", "downloads.kind.emulator", "", "", "/static/clients/wharf.svg"},
	{"Pier", "PS3", "downloads.kind.emulator", "", "", "/static/clients/pier.svg"},
}

var downloads = []download{
	{"OpenPak setup", "Switch", "downloads.kind.setup", "", "", ""},
	{"Inkay", "Wii U", "downloads.kind.patch", "", "https://github.com/openpak/nn-inkay/releases/latest", ""},
	{"nimbus", "3DS", "downloads.kind.patch", "", "https://github.com/openpak/nn-nimbus/releases/latest", ""},
	{"WFC patcher", "Wii", "downloads.kind.patch", "", "https://github.com/openpak/nn-wfc-patcher-wii/releases/latest", ""},
	{"WFC patcher", "DS", "downloads.kind.patch", "", "", ""},
	{"OpenPak patch", "Xbox", "downloads.kind.patch", "", "", ""},
	{"OpenPak patch", "Xbox 360", "downloads.kind.patch", "", "", ""},
	{"OpenPak patch", "PS3", "downloads.kind.patch", "", "", ""},
}

// platform is one console family and how far OpenPak is with it; the status words and
// colours are the titles list's.
type platform struct{ Name, Console, Status string }

var platforms = []platform{
	{"Nintendo Switch", "Switch", "live"},
	{"Wii U", "Wii U", "live"},
	{"Nintendo 3DS", "3DS", "live"},
	{"Wii", "Wii", "live"},
	{"Nintendo DS", "DS", "live"},
	{"Xbox", "Xbox", "live"},
	{"PlayStation 3", "PS3", "live"},
	{"Xbox 360", "Xbox 360", "live"},
	{"PlayStation Portable", "PSP", "exploring"},
	{"PlayStation Vita", "Vita", "exploring"},
	{"PlayStation 4", "", "exploring"},
	{"PlayStation 5", "", "planned"},
	{"Xbox One · Series", "", "not-feasible"},
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	s.render(w, http.StatusOK, "downloads.html", struct {
		baseData
		Downloads []download
		Emulators []download
		Platforms []platform
	}{s.data(r, v, i18n.T(i18n.FromRequest(r), "downloads.title")), downloads, emulators, platforms})
}

func (s *Server) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	if v != nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	d := s.data(r, nil, i18n.T(i18n.FromRequest(r), "nav.register"))
	type reg struct {
		baseData
		Closed      bool
		Countries   []country
		MaxBirthday string
	}
	// No date in the future, and no JavaScript to enforce it.
	s.render(w, http.StatusOK, "register.html", reg{d, !d.RegOpen, countries, time.Now().Format("2006-01-02")})
}

func (s *Server) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.DB.GetSiteConfig(r.Context())
	if err == nil && !cfg.RegistrationsOpen {
		s.render(w, http.StatusForbidden, "message.html", struct {
			baseData
			Message string
		}{s.data(r, nil, i18n.T(i18n.FromRequest(r), "nav.register")), i18n.T(i18n.FromRequest(r), "auth.register.closed")})
		return
	}
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	display := strings.TrimSpace(r.PostFormValue("display_name"))
	country := strings.TrimSpace(r.PostFormValue("country"))
	birthday := strings.TrimSpace(r.PostFormValue("birthday"))
	// Enumeration resistance is enforced by the core: taken-but-unverified
	// emails resend, and the response shape is identical.
	if err := s.Core.RegisterWithProfile(r.Context(), email, password, display, r.PostFormValue("handle"), country, birthday); err != nil {
		if message := core.HandleErrorMessage(err); message != "" {
			s.render(w, http.StatusBadRequest, "message.html", struct {
				baseData
				Message string
			}{s.data(r, nil, "Choose a handle"), message})
			return
		}
		s.log.Info("register rejected", "err", err)
	}
	s.render(w, http.StatusOK, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, i18n.T(i18n.FromRequest(r), "nav.register")), i18n.T(i18n.FromRequest(r), "auth.register.sent")})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	err := s.Core.VerifyEmail(r.Context(), r.URL.Query().Get("token"))
	msg := i18n.T(i18n.FromRequest(r), "auth.verify.ok")
	if err != nil {
		msg = i18n.T(i18n.FromRequest(r), "auth.verify.bad")
	}
	s.render(w, http.StatusOK, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, i18n.T(i18n.FromRequest(r), "page.verify")), msg})
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if v := s.viewerOf(w, r); v != nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login.html", s.data(r, nil, i18n.T(i18n.FromRequest(r), "auth.login.title")))
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")

	// WEB-01/WEB-02: log in through the core's HTTP route and bind the website
	// cookie to the core session id. A 2FA account gets a challenge instead of
	// a session, and the second step asks for the code.
	coreSession, err := s.Core.Login(r.Context(), email, password)
	var challenge *core.LoginChallenge
	if errors.As(err, &challenge) {
		s.renderLogin2FA(w, r, http.StatusOK, challenge.Challenge, "")
		return
	}
	if err != nil {
		s.loginFailed(w, r, http.StatusUnauthorized, "auth.login.failed")
		return
	}
	s.finishLogin(w, r, coreSession)
}

// login2FAView is the second sign-in step. The challenge is the core's own:
// ten minutes, single use, burned after five wrong codes, and worthless
// without the code, so a hidden field is as safe a place for it as any.
type login2FAView struct {
	baseData
	Challenge string
	Error     string
}

func (s *Server) renderLogin2FA(w http.ResponseWriter, r *http.Request, status int, challenge, errKey string) {
	v := login2FAView{baseData: s.data(r, nil, i18n.T(i18n.FromRequest(r), "auth.2fa.title")), Challenge: challenge}
	if errKey != "" {
		v.Error = i18n.T(i18n.FromRequest(r), errKey)
	}
	s.render(w, status, "login_2fa.html", v)
}

// handleLogin2FASubmit is the second step: the challenge from the first and a
// code from the authenticator app, or one recovery code.
func (s *Server) handleLogin2FASubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	challenge := r.PostFormValue("challenge")
	code, recovery := accounts.SplitSecondFactor(r.PostFormValue("code"))
	if challenge == "" || (code == "" && recovery == "") {
		s.renderLogin2FA(w, r, http.StatusBadRequest, challenge, "auth.2fa.missing")
		return
	}
	coreSession, err := s.Core.Login2FA(r.Context(), challenge, code, recovery)
	switch {
	case err == nil:
		s.finishLogin(w, r, coreSession)
	case errors.Is(err, core.ErrInvalidCode):
		// A wrong code and a spent challenge look the same from here; the
		// core burns the challenge after five tries, and then only a fresh
		// sign-in helps, which the page offers.
		s.renderLogin2FA(w, r, http.StatusUnauthorized, challenge, "auth.2fa.wrong")
	case errors.Is(err, core.ErrTwoFactorLocked):
		s.loginFailed(w, r, http.StatusTooManyRequests, "auth.2fa.locked")
	default:
		s.log.Error("login 2fa", "err", err)
		s.loginFailed(w, r, http.StatusBadGateway, "msg.error")
	}
}

func (s *Server) loginFailed(w http.ResponseWriter, r *http.Request, status int, key string) {
	s.render(w, status, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, i18n.T(i18n.FromRequest(r), "auth.login.title")), i18n.T(i18n.FromRequest(r), key)})
}

// finishLogin turns a fresh core session into a website session. Whoami
// (CR-4) names the account; the password is never checked a second time.
func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, coreSession string) {
	acct, err := s.Core.Whoami(r.Context(), coreSession)
	if err != nil {
		// Login worked core-side but the session could not be resolved; keep
		// the user signed out rather than serving a half session.
		_ = s.Core.Logout(context.Background(), coreSession)
		s.log.Error("whoami", "err", err)
		s.loginFailed(w, r, http.StatusInternalServerError, "msg.error")
		return
	}
	if acct.Status == "banned" {
		_ = s.Core.Logout(context.Background(), coreSession)
		s.loginFailed(w, r, http.StatusForbidden, "auth.login.banned")
		return
	}
	if err := s.DB.PutWebSession(r.Context(), store.WebSession{
		CoreSessionID: coreSession, AccountID: acct.ID, Email: acct.Email, DisplayName: acct.DisplayName,
	}); err != nil {
		_ = s.Core.Logout(context.Background(), coreSession)
		s.log.Error("put web session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Sessions.SetCookie(w, coreSession)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	if v != nil {
		if err := r.ParseForm(); err != nil || !s.Sessions.CheckCSRF(v.CoreSessionID, r.PostFormValue("csrf")) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		if err := s.Core.Logout(r.Context(), v.CoreSessionID); err != nil {
			s.log.Info("logout", "err", err)
		}
		_ = s.DB.DeleteWebSession(r.Context(), v.CoreSessionID)
	}
	s.Sessions.ClearCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleForgotForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "forgot.html", s.data(r, nil, i18n.T(i18n.FromRequest(r), "auth.forgot.title")))
}

func (s *Server) handleForgotSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err == nil {
		// Core-side enumeration resistance: identical response either way.
		_ = s.Core.ForgotPassword(r.Context(), normalizeEmail(r.PostFormValue("email")))
	}
	s.render(w, http.StatusOK, "message.html", struct {
		baseData
		Message string
	}{s.data(r, nil, i18n.T(i18n.FromRequest(r), "auth.forgot.title")), i18n.T(i18n.FromRequest(r), "auth.forgot.sent")})
}

func (s *Server) handleResetForm(w http.ResponseWriter, r *http.Request) {
	type reset struct {
		baseData
		Token string
	}
	s.render(w, http.StatusOK, "reset.html", reset{s.data(r, nil, i18n.T(i18n.FromRequest(r), "auth.reset.title")), r.URL.Query().Get("token")})
}

func (s *Server) handleResetSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	err := s.Core.ResetPassword(r.Context(), r.PostFormValue("token"), r.PostFormValue("password"))
	if err != nil {
		s.render(w, http.StatusOK, "message.html", struct {
			baseData
			Message string
		}{s.data(r, nil, i18n.T(i18n.FromRequest(r), "auth.forgot.title")), i18n.T(i18n.FromRequest(r), "auth.reset.bad")})
		return
	}
	// The core revoked every web session on reset; the website must forget
	// its index rows and bearer tokens for this account too. We do not know
	// the account id without the password, so expiry + core-side revocation
	// do the enforcement; local rows die on next use.
	redirectMsg(w, r, "/login", "pwreset")
}

func clientKey(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// fmtFriendCode renders a link subject as SW- plus dash-separated groups of
// four when the adapter stored 12 or 16 hex digits (both are in the wild;
// the subject is adapter-owned, CR-3). Anything else is shown verbatim.
// Deprecated: a link subject is a console user id, not a friend code. Kept
// only for the Wii U/3DS namespaces whose subjects really are hex ids.
func fmtFriendCode(subject string) string {
	hex := make([]rune, 0, 16)
	for _, c := range subject {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			hex = append(hex, c)
		case c == '-':
		default:
			return subject
		}
	}
	if len(hex) != 12 && len(hex) != 16 {
		return subject
	}
	groups := make([]string, 0, 4)
	for i := 0; i < len(hex); i += 4 {
		groups = append(groups, string(hex[i:i+4]))
	}
	return "SW-" + strings.Join(groups, "-")
}

// AssetVersion is the fingerprint appended to static URLs; admin templates share the layout,
// so they need it too.
func AssetVersion() string { return assetVersion() }

// assetVersion hashes the files served from web/static, once per process. A new deploy has a
// new binary, so a new fingerprint, so browsers and caches fetch the new files.
var assetVersion = sync.OnceValue(func() string {
	h := sha256.New()
	filepath.WalkDir("web/static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil {
			h.Write([]byte(path))
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
})

// titleArt is the cover tiles in web/static/art/titles (scripts/title-art), listed once
// per process like assetVersion: a deploy that adds tiles is a new process.
var titleArt = sync.OnceValue(func() titleart.Set {
	return titleart.Load(filepath.Join("web/static", titleart.Dir))
})

// artPath is the URL of a non-Switch title's cover tile, fingerprinted like every static
// file, or "" when it has none and draws its console's mark.
func artPath(console, name string) string {
	if p := titleArt().Path(console, name); p != "" {
		return p + "?v=" + assetVersion()
	}
	return ""
}

// adapterProxy forwards the console-link pages to the Switch adapter, so they are served under
// openpak.org's certificate. Only /link is proxied: nothing else of the adapter is exposed.
func adapterProxy(target string) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "link service is not configured", http.StatusServiceUnavailable)
		})
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "the link service is unavailable", http.StatusBadGateway)
	}
	return proxy
}

// country is one option in the registration list. The console sends a two-letter code with
// every account it links, so the list is codes first and names second.
type country struct {
	Code string
	Name string
}

// A short list rather than all 249: these are the places this network is actually used, and a
// wrong pick is editable later. ponytail: extend when someone asks.
var countries = []country{
	{"GB", "United Kingdom"}, {"IE", "Ireland"}, {"US", "United States"}, {"CA", "Canada"},
	{"AU", "Australia"}, {"NZ", "New Zealand"}, {"FR", "France"}, {"DE", "Germany"},
	{"ES", "Spain"}, {"IT", "Italy"}, {"PT", "Portugal"}, {"NL", "Netherlands"},
	{"BE", "Belgium"}, {"SE", "Sweden"}, {"NO", "Norway"}, {"DK", "Denmark"},
	{"FI", "Finland"}, {"PL", "Poland"}, {"BR", "Brazil"}, {"MX", "Mexico"},
	{"JP", "Japan"}, {"KR", "South Korea"},
}

// handleLang stores the footer's language choice and returns to the page it
// came from (same-site paths only).
func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	if l := i18n.Match(r.URL.Query().Get("l")); l != "" {
		http.SetCookie(w, &http.Cookie{Name: i18n.CookieName, Value: l, Path: "/", MaxAge: 365 * 24 * 3600,
			HttpOnly: true, Secure: s.Sessions.Secure, SameSite: http.SameSiteLaxMode})
	}
	back := r.URL.Query().Get("back")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.HasPrefix(back, "/\\") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
