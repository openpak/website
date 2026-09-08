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
)

// accountView is the profile page model (WEB-02, WEB-03).
type accountView struct {
	baseData
	FriendCode     string
	HasFriendCode  bool
	Links          []linkRow
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
		view.HasFriendCode = true
		view.FriendCode = fmtFriendCode(link.SubjectID)
		view.Links = append(view.Links, linkRow{Namespace: "Switch", Subject: view.FriendCode, LinkID: link.LinkID})
	}

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

	// Avatar existence check is cheap and local.
	if s.Media != nil {
		if _, err := s.Media.Avatar(ctx, v.AccountID, media.SizeSmall); err != nil {
			view.AvatarURL = ""
		}
	} else {
		view.AvatarPending = true
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
	if err := s.Core.UpdateProfile(r.Context(), v.CoreSessionID, country, birthday); err != nil {
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
	key := "unlink:" + v.AccountID + ":" + linkID
	if err := s.Links.Unlink(r.Context(), linkID, key); err != nil {
		s.log.Error("unlink", "err", err)
		http.Error(w, "could not revoke link", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account", "saved")
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
		http.NotFound(w, r)
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
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	coreOK := s.Core.Healthy(ctx) == nil
	dbOK := s.DB.Ping(ctx) == nil
	storeOK := s.Media == nil || s.Media.Healthy(ctx) == nil

	type statusView struct {
		baseData
		CoreOK, DBOk, StorageOK bool
	}
	view := statusView{s.data(r, s.viewerOf(w, r), i18n.T("en", "status.title")), coreOK, dbOK, storeOK}
	s.render(w, http.StatusOK, "status.html", view)
}
