package pages

import (
	"errors"
	"net/http"
	"strconv"

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
	file, _, err := r.FormFile("avatar")
	if err != nil {
		http.Error(w, "avatar file required", http.StatusBadRequest)
		return
	}
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
	if err := s.DB.UpsertMedia(r.Context(), store.MediaRecord{AccountID: v.AccountID, Kind: "avatar"}); err != nil {
		s.log.Error("media metadata", "err", err)
	}
	redirectMsg(w, r, "/account", "saved")
}

// handleAvatarServe serves stored avatars. Console delivery will move to
// the adapter once CR-2 lands; the website keeps serving its own copies.
func (s *Server) handleAvatarServe(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		http.NotFound(w, r)
		return
	}
	accountID := r.PathValue("accountID")
	size, err := strconv.Atoi(r.PathValue("size"))
	if err != nil || (size != media.SizeSmall && size != media.SizeLarge) {
		http.NotFound(w, r)
		return
	}
	data, err := s.Media.Avatar(r.Context(), accountID, size)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(data)
}

// --- Friends (WEB-04): every write goes through the Social contract. ---

type friendsView struct {
	baseData
	Friends, Incoming, Outgoing []core.Friend
	BlockedNote                 []string // ids the viewer blocked
}

func (s *Server) handleFriends(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	ctx := r.Context()
	view := friendsView{baseData: s.data(r, v, i18n.T("en", "friends.title"))}
	view.Friends, _ = s.Social.ListFriends(ctx, v.AccountID)
	view.Incoming, _ = s.Social.ListIncoming(ctx, v.AccountID)
	view.Outgoing, _ = s.Social.ListOutgoing(ctx, v.AccountID)
	s.render(w, http.StatusOK, "friends.html", view)
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
		redirectMsg(w, r, "/account/friends", "funknown")
		return
	}
	if target == v.AccountID {
		redirectMsg(w, r, "/account/friends", "fself")
		return
	}
	if err := s.Social.RequestFriend(r.Context(), v.AccountID, target); err != nil {
		s.log.Error("request friend", "err", err)
		http.Error(w, "could not send request", http.StatusInternalServerError)
		return
	}
	redirectMsg(w, r, "/account/friends", "frequest")
}

// normalizeFriendCode strips display formatting: SW-1234-5678-9012 and
// 123456789012 both arrive at the contract as the stored subject.
func normalizeFriendCode(code string) string {
	out := make([]rune, 0, len(code))
	for _, c := range code {
		if c != '-' && c != ' ' {
			out = append(out, c)
		}
	}
	s := string(out)
	// The "SW" prefix is display-only; the adapter subject is 12 chars.
	if len(s) == 14 && (s[:2] == "SW" || s[:2] == "sw") {
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
	redirectMsg(w, r, "/account/friends", "saved")
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
	redirectMsg(w, r, "/account/friends", "saved")
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
	redirectMsg(w, r, "/account/friends", "saved")
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
	redirectMsg(w, r, "/account/friends", "saved")
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
	redirectMsg(w, r, "/account/friends", "saved")
}

// handleInvitations is WEB-05. Read-only view pending CR-1; the page
// renders a clear notice instead of pretending.
func (s *Server) handleInvitations(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	type invView struct {
		baseData
		Invites      []core.PendingInvitation
		ContractWait bool
	}
	view := invView{baseData: s.data(r, v, i18n.T("en", "invitations.title"))}
	inv, err := s.Invites.ListPending(r.Context(), v.AccountID)
	if errors.Is(err, core.ErrContractPending) {
		view.ContractWait = true
	} else if err != nil {
		s.log.Error("list invitations", "err", err)
	}
	view.Invites = inv
	s.render(w, http.StatusOK, "invitations.html", view)
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
