package pages

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/wfc"
)

// accountView is the profile page model (WEB-02, WEB-03).
type accountView struct {
	baseData
	FriendCode     string
	HasFriendCode  bool
	Links          []linkRow
	CanLinkWFC     bool // a Wii/DS server is configured, so the friend-code form is shown
	Sessions       []core.Session
	CurrentSession string
	AvatarURL      string
	AvatarPending  bool
	Presets        []string
	Country        string
	Birthday       string
	Countries      []country
	MaxBirthday    string
	friendsData
}

type linkRow struct {
	Namespace string
	Subject   string
	LinkID    string
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	ctx := r.Context()
	view := accountView{baseData: s.data(r, v, i18n.T("en", "account.title")), CurrentSession: v.CoreSessionID}
	view.AvatarURL = "/media/avatars/" + v.AccountID + "/512"

	// Friend code: read-only projection of the adapter-owned link subject
	// (CR-3 interim). Absence is normal for accounts without a console.
	if link, err := s.Links.ActiveLinkByAccount(ctx, "switch", v.AccountID); err == nil {
		// The subject is the console's own user id. The friend code is a
		// separate thing the adapter mints -- twelve digits, unique, the one
		// people read out to each other -- so ask for it rather than
		// formatting the id into something that merely looks like one.
		view.FriendCode = s.Adapter.FriendCode(ctx, v.AccountID)
		view.HasFriendCode = view.FriendCode != ""
		view.Links = append(view.Links, linkRow{Namespace: "Switch", Subject: link.SubjectID, LinkID: link.LinkID})
	}

	// Wii and DS cannot link themselves; the website did it from a friend code (handleLinkWFC).
	if link, err := s.Links.ActiveLinkByAccount(ctx, "wfc", v.AccountID); err == nil {
		view.Links = append(view.Links, linkRow{Namespace: "Wii / DS", Subject: link.SubjectID, LinkID: link.LinkID})
	}
	view.CanLinkWFC = s.WFC != nil

	sessions, err := s.Core.ListSessions(ctx, v.CoreSessionID)
	if err != nil {
		s.log.Error("list sessions", "err", err)
	}
	view.Sessions = sessions
	view.friendsData = s.loadFriends(ctx, v.AccountID)
	view.Presets = media.PresetIDs
	// The console's user object carries both, and accounts made before registration asked for
	// them hold neither; the form below is the only way to fill them in.
	view.Countries, view.MaxBirthday = countries, time.Now().Format("2006-01-02")
	if acct, err := s.Identity.GetAccount(ctx, v.AccountID); err == nil {
		view.Country, view.Birthday = acct.Country, acct.Birthday
	} else {
		s.log.Error("get account", "err", err)
	}

	// No existence check: the URL always resolves, to the account's own
	// picture or to the preset its id picks.
	if s.Media == nil {
		view.AvatarURL, view.AvatarPending = "", true
	}
	s.render(w, http.StatusOK, "account.html", view)
}

// handleChangePassword rotates the password of the signed-in account. The core requires the
// current one and revokes the account's other sessions, so this page only has to report back.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")
	if next != r.PostFormValue("confirm_password") {
		redirectMsg(w, r, "/account", "password-mismatch")
		return
	}
	if err := s.Core.ChangePassword(r.Context(), v.CoreSessionID, current, next); err != nil {
		redirectMsg(w, r, "/account", "password-failed")
		return
	}
	redirectMsg(w, r, "/account", "password-changed")
}

// handleUpdateProfile saves the country and date of birth. Both are optional here: an account
// that holds neither is valid, and nothing downstream substitutes a value for what is missing.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	country := strings.TrimSpace(r.PostFormValue("country"))
	birthday := strings.TrimSpace(r.PostFormValue("birthday"))
	if err := s.Core.UpdateProfile(r.Context(), v.CoreSessionID, country, birthday, ""); err != nil {
		s.log.Error("update profile", "err", err)
		redirectMsg(w, r, "/account", "profile-failed")
		return
	}
	redirectMsg(w, r, "/account", "profile-saved")
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	id := r.PostFormValue("id")
	err := s.Core.RevokeSession(r.Context(), v.CoreSessionID, id)
	switch {
	case err == nil:
		// If the user revoked the device they are on, the core session is
		// gone; drop local state and send them to login.
		if id == v.CoreSessionID {
			_ = s.DB.DeleteWebSession(r.Context(), v.CoreSessionID)
			s.Sessions.ClearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		redirectMsg(w, r, "/account", "saved")
	case errors.Is(err, core.ErrNotFound):
		http.Error(w, "session not found", http.StatusNotFound)
	default:
		s.log.Error("revoke session", "err", err)
		http.Error(w, "could not revoke session", http.StatusInternalServerError)
	}
}

// handleUnlink revokes the console-created link (journey 1).
func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	linkID := r.PostFormValue("link_id")
	if linkID == "" {
		http.Error(w, "link required", http.StatusBadRequest)
		return
	}
	// The form names a link id; only one of the viewer's own links may be revoked.
	owned := false
	for _, ns := range []string{"switch", "wfc"} {
		if l, err := s.Links.ActiveLinkByAccount(r.Context(), ns, v.AccountID); err == nil && l.LinkID == linkID {
			owned = true
		}
	}
	if !owned {
		http.Error(w, "link not found", http.StatusNotFound)
		return
	}
	key := "unlink:" + v.AccountID + ":" + linkID
	if err := s.Links.Unlink(r.Context(), linkID, key); err != nil {
		s.log.Error("unlink", "err", err)
		http.Error(w, "could not revoke link", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "saved")
}

// handleLinkWFC links a Wii or DS from a friend code the player read off a game. WFC has no
// login and no way to type a code on the console, so this is the only link path; the core
// holds the link ("wfc", console user id) and nn-wfc stores nothing.
func (s *Server) handleLinkWFC(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if s.WFC == nil {
		http.Error(w, "Wii/DS linking is not available", http.StatusServiceUnavailable)
		return
	}
	profile, err := s.WFC.Resolve(r.Context(), r.PostFormValue("friend_code"))
	switch {
	case errors.Is(err, wfc.ErrNotFound):
		redirectMsg(w, r, "/account", "wfc-notfound")
		return
	case err != nil:
		s.log.Error("wfc resolve", "err", err)
		http.Error(w, "could not reach the Wii/DS server", http.StatusBadGateway)
		return
	}
	subject := strconv.FormatUint(profile.UserID, 10)
	err = s.Links.Link(r.Context(), v.AccountID, "wfc", subject, "wfc:"+v.AccountID+":"+subject)
	switch {
	case err == nil:
		redirectMsg(w, r, "/account", "wfc-linked")
	case errors.Is(err, core.ErrConflict):
		redirectMsg(w, r, "/account", "wfc-taken")
	default:
		s.log.Error("wfc link", "err", err)
		http.Error(w, "could not link", http.StatusInternalServerError)
	}
}

func (s *Server) handleAvatarUpload(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if s.Media == nil {
		s.render(w, http.StatusServiceUnavailable, "message.html", struct {
			baseData
			Message string
		}{s.data(r, v, "Avatar"), i18n.T("en", "account.avatar.pending")})
		return
	}
	if !s.MediaLimit.Allow(v.AccountID) {
		http.Error(w, "too many uploads; slow down", http.StatusTooManyRequests)
		return
	}
	// One form, two ways to choose: upload a file, or pick a built-in. An
	// uploaded file wins — it is the more deliberate act, and the tiles
	// cannot be made mutually exclusive without script.
	if file, _, err := r.FormFile("avatar"); err == nil {
		defer file.Close()
		if err := s.Media.StoreAvatar(r.Context(), v.AccountID, file); err != nil {
			if errors.Is(err, media.ErrInvalid) {
				http.Error(w, "upload a PNG or JPEG up to 4 MiB", http.StatusBadRequest)
				return
			}
			s.log.Error("store avatar", "err", err)
			http.Error(w, "could not store avatar", http.StatusInternalServerError)
			return
		}
	} else if preset := r.PostFormValue("preset"); preset != "" {
		if err := s.Media.StorePreset(r.Context(), v.AccountID, preset); err != nil {
			if errors.Is(err, media.ErrInvalid) {
				http.Error(w, "unknown avatar", http.StatusBadRequest)
				return
			}
			s.log.Error("store preset avatar", "err", err)
			http.Error(w, "could not store avatar", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "choose an avatar or upload a file", http.StatusBadRequest)
		return
	}
	// UpdatedAt is the version the console path needs: the adapter builds
	// its ?v= cache key from it, so a stale value means a stale picture on
	// the console (CR-2).
	if err := s.DB.UpsertMedia(r.Context(), store.MediaRecord{
		AccountID: v.AccountID, Kind: "avatar", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		s.log.Error("media metadata", "err", err)
	}
	redirectMsg(w, r, "/account", "saved")
}

// handleAvatarServe serves the website's own PNG copies, the console JPEG,
// and previews of the built-in avatars.
func (s *Server) handleAvatarServe(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		http.NotFound(w, r)
		return
	}
	accountID, size := r.PathValue("accountID"), r.PathValue("size")

	// Preset previews are account-independent and immutable.
	if accountID == "preset" {
		data, err := media.PresetImage(strings.TrimSuffix(size, ".png"), 128)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		_, _ = w.Write(data)
		return
	}

	// The console JPEG (CR-2 interim): served with a validator so a refresh
	// costs a 304 when nothing changed.
	if size == "console.jpg" {
		data, err := s.Media.ConsoleAvatar(r.Context(), accountID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		s.writeWithValidators(w, r, accountID, data)
		return
	}

	px, err := strconv.Atoi(size)
	if err != nil || (px != media.SizeSmall && px != media.SizeLarge) {
		http.NotFound(w, r)
		return
	}
	data, err := s.Media.Avatar(r.Context(), accountID, px)
	if err != nil {
		// Never a 404: every account has a picture, chosen from its id until
		// somebody picks another. A missing avatar used to mean a blank
		// initial on the site and the generic default on a console.
		data, err = media.PresetImage(media.PresetForAccount(accountID), px)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	s.writeWithValidators(w, r, accountID, data)
}

// writeWithValidators stamps ETag/Last-Modified from the avatar's recorded
// update time and answers conditional requests with 304, so a console or
// browser re-checking after a refresh transfers nothing when nothing moved.
func (s *Server) writeWithValidators(w http.ResponseWriter, r *http.Request, accountID string, data []byte) {
	rec, err := s.DB.GetMedia(r.Context(), accountID, "avatar")
	if err == nil && !rec.UpdatedAt.IsZero() {
		etag := fmt.Sprintf("\"%d\"", rec.UpdatedAt.UnixNano())
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", rec.UpdatedAt.UTC().Format(http.TimeFormat))
		if match := r.Header.Get("If-None-Match"); match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if since, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil &&
			!rec.UpdatedAt.Truncate(time.Second).After(since) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	_, _ = w.Write(data)
}

// --- Friends (WEB-04): every write goes through the Social contract. ---

// friendsData is the friend-graph half of the account page: friends,
// pending requests in both directions, and invitations (WEB-04, WEB-05).
type friendsData struct {
	Friends, Incoming, Outgoing []core.Friend
	BlockedNote                 []string // ids the viewer blocked
	Invites                     []core.PendingInvitation
}

// handleFriends keeps the old URL working; friends are a section of the
// account page now, not a page of their own.
func (s *Server) handleFriends(w http.ResponseWriter, r *http.Request) {
	if v := s.requireViewer(w, r); v == nil {
		return
	}
	http.Redirect(w, r, "/account#friends", http.StatusSeeOther)
}

// loadFriends fills the friend-graph half of the account page.
func (s *Server) loadFriends(ctx context.Context, accountID string) friendsData {
	var d friendsData
	d.Friends, _ = s.Social.ListFriends(ctx, accountID)
	d.Incoming, _ = s.Social.ListIncoming(ctx, accountID)
	d.Outgoing, _ = s.Social.ListOutgoing(ctx, accountID)
	// Read-only here: the core's ListInvitations consumes nothing, so showing
	// someone their inbox cannot disturb delivery to their console.
	inv, err := s.Invites.ListPending(ctx, accountID)
	if err != nil {
		s.log.Error("list invitations", "err", err)
	}
	d.Invites = inv
	return d
}

func (s *Server) handleFriendRequest(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	code := normalizeFriendCode(r.PostFormValue("friend_code"))
	if code == "" {
		http.Error(w, "friend code required", http.StatusBadRequest)
		return
	}
	// Resolve the code to an account through the Links contract: the code
	// IS the adapter-owned link subject (CR-3 interim).
	target, err := s.Links.AccountBySubject(r.Context(), "switch", code)
	if err != nil || target == "" {
		redirectMsg(w, r, "/account", "funknown")
		return
	}
	if target == v.AccountID {
		redirectMsg(w, r, "/account", "fself")
		return
	}
	if err := s.Social.RequestFriend(r.Context(), v.AccountID, target); err != nil {
		s.log.Error("request friend", "err", err)
		http.Error(w, "could not send request", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "frequest")
}

// normalizeFriendCode strips display formatting: SW-1234-5678-9012,
// sw123456789012 and 1234-5678-9012 all arrive at the contract as the
// stored subject — 12 lower-case hex digits.
func normalizeFriendCode(code string) string {
	out := make([]rune, 0, len(code))
	for _, c := range code {
		if c != '-' && c != ' ' {
			out = append(out, c)
		}
	}
	// Subjects are stored lower-case; the field accepts either case.
	s := strings.ToLower(string(out))
	// The "sw" prefix is display-only; adapter subjects are 12 or 16 chars.
	if (len(s) == 14 || len(s) == 18) && strings.HasPrefix(s, "sw") {
		s = s[2:]
	}
	return s
}

func (s *Server) handleFriendAccept(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if err := s.Social.AcceptFriend(r.Context(), v.AccountID, r.PostFormValue("account_id")); err != nil {
		s.log.Error("accept friend", "err", err)
		http.Error(w, "could not accept request", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "saved")
}

func (s *Server) handleFriendDecline(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	// Decline = remove the pending row (RemoveFriend covers pending).
	if err := s.Social.RemoveFriend(r.Context(), v.AccountID, r.PostFormValue("account_id")); err != nil {
		s.log.Error("decline friend", "err", err)
		http.Error(w, "could not decline request", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "saved")
}

func (s *Server) handleFriendRemove(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if err := s.Social.RemoveFriend(r.Context(), v.AccountID, r.PostFormValue("account_id")); err != nil {
		s.log.Error("remove friend", "err", err)
		http.Error(w, "could not remove friend", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "saved")
}

func (s *Server) handleFriendBlock(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if err := s.Social.Block(r.Context(), v.AccountID, r.PostFormValue("account_id")); err != nil {
		s.log.Error("block", "err", err)
		http.Error(w, "could not block", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "saved")
}

func (s *Server) handleFriendUnblock(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if err := s.Social.Unblock(r.Context(), v.AccountID, r.PostFormValue("account_id")); err != nil {
		s.log.Error("unblock", "err", err)
		http.Error(w, "could not unblock", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "saved")
}

// handleInvitations keeps the old URL working: invitations are a section
// of the friends page (WEB-05), not a separate destination.
func (s *Server) handleInvitations(w http.ResponseWriter, r *http.Request) {
	if v := s.requireViewer(w, r); v == nil {
		return
	}
	http.Redirect(w, r, "/account/friends#invitations", http.StatusSeeOther)
}

// handleStatus is the public status page (journey 5).
// handleStatus is the network's own status page: what is running, and who is
// playing what. The availability page on a separate host answers "is OpenPak
// reachable at all"; this one answers "is my game up and is anyone on", which
// is the question a person actually arrives with — and it can afford to be
// richer precisely because the other page exists for when this one cannot load.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	coreOK := s.Core.Healthy(ctx) == nil
	dbOK := s.DB.Ping(ctx) == nil
	storeOK := s.Media == nil || s.Media.Healthy(ctx) == nil

	type titleRow struct {
		Name    string
		Players int
	}
	type serverRow struct {
		Name      string
		Players   int
		Checkable bool
		Up        bool
	}
	type networkRow struct {
		Name, State string
		Live        bool
		Players     int
	}
	type statusView struct {
		baseData
		CoreOK, DBOk, StorageOK bool
		Operational             bool
		Networks                []networkRow
		Servers                 []serverRow
		Titles                  []titleRow
		TotalPlayers            int
		CountsOK                bool
	}
	view := statusView{
		baseData:    s.data(r, s.viewerOf(w, r), i18n.T("en", "status.title")),
		CoreOK:      coreOK,
		DBOk:        dbOK,
		StorageOK:   storeOK,
		Operational: coreOK && dbOK && storeOK,
	}
	// Player counts come from the live session leases, so an empty list means
	// nobody is playing rather than that something is broken. A failure to
	// read them is shown as its own thing, not as an outage.
	var perNetwork map[string]int
	var titlesRaw []core.TitlePlayers
	if s.Invites != nil {
		if titles, nets, total, err := s.Invites.PlayerCounts(ctx); err == nil {
			titlesRaw = titles
			view.CountsOK, view.TotalPlayers, perNetwork = true, total, nets
			for _, t := range titles {
				view.Titles = append(view.Titles, titleRow{Name: titleName(t.TitleID), Players: t.Players})
			}
		} else {
			s.log.Error("player counts", "err", err)
		}
	}
	// The game servers themselves, so a title that nobody happens to be playing
	// still shows up: "no players" and "not running" are different answers and
	// a status page has to tell them apart.
	byTitle := map[string]int{}
	for _, t := range titlesRaw {
		byTitle[t.TitleID] = t.Players
	}
	for _, g := range gameServers() {
		row := serverRow{Name: g.Name, Players: byTitle[g.TitleID], Checkable: g.Health != ""}
		if row.Checkable {
			row.Up = healthy(ctx, g.Health)
		}
		view.Servers = append(view.Servers, row)
	}

	// Every network we intend to replace, with numbers only where something is
	// actually deployed. A network still being written says so; showing it as
	// green because nothing has failed yet would be the one lie a status page
	// must not tell.
	for _, n := range networks {
		row := networkRow{Name: n.Name, State: n.State, Live: n.State == "live"}
		if n.Namespace != "" {
			row.Players = perNetwork[n.Namespace]
		}
		view.Networks = append(view.Networks, row)
	}
	s.render(w, http.StatusOK, "status.html", view)
}
