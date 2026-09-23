// Package api serves the versioned mobile API under /api/v1 (WEB-08).
// Bearer tokens are minted from a core web session: the client performs a
// password login, the website establishes the core session through the
// core's HTTP route and stores a hashed bearer bound to it. Every bearer
// request re-validates the bound core session, so bans, logouts and
// password resets end mobile access within the core's revocation window.
package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/netprofile"
	"github.com/openpak/website/internal/news"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/wfc"
)

// TokenTTL is the mobile bearer lifetime; it can never outlive its bound
// core web session.
const TokenTTL = 30 * 24 * time.Hour

// Server hosts the /api/v1 routes.
type Server struct {
	Core     core.HTTP
	Identity core.Identity
	Social   core.Social
	Links    core.Links
	Invites  core.InvitationsView
	// Federation is set by main; nil answers 503 on the region routes.
	Federation core.Federation
	Media      *media.Service
	// Profiles generates the network profile (PRD prds/network-profile-service-prd.md)
	// from the live Traefik routing. Set by main when NETPROFILE_TRAEFIK_FILE
	// is configured; nil answers 503 on /api/v1/network/*.
	Profiles *netprofile.Service
	// Adapter answers the console-shaped questions the core cannot: the friend
	// code is minted by the Switch adapter and lives only there.
	Adapter   *adapter.Client
	NNAdapter *adapter.Client // the Wii U/3DS adapter (nn-account), nil when not configured
	Saves     *saves.Client
	// SaveTitle names a save's game and says whether /titles/icon/{id} draws it; the pages
	// package owns the lookup. Nil lists bare title ids.
	SaveTitle func(ctx context.Context, platform, id string) (name string, icon bool)
	Chat      *chat.Client
	WFC       *wfc.Client // resolves Wii/DS friend codes; nil answers 503 on /me/links/wfc
	// News is the public news feed; nil answers an empty list on /news.
	News       *news.Feed
	MediaLimit *ratelimit.Limiter
	DB         store.Store
	Sessions   *session.Manager
	AdminIDs   []string           // WEBSITE_ADMIN_ACCOUNT_IDS: may write the catalog with a session
	Auth       *ratelimit.Limiter // per IP, token minting
	PerToken   *ratelimit.Limiter // per token, API calls
	log        *slog.Logger
}

// New builds the API server.
func New(c core.HTTP, id core.Identity, soc core.Social, lnk core.Links, inv core.InvitationsView, db store.Store, sess *session.Manager, md *media.Service, log *slog.Logger) *Server {
	return &Server{
		Core: c, Identity: id, Social: soc, Links: lnk, Invites: inv, DB: db, Sessions: sess,
		Media:      md,
		Adapter:    adapter.New(os.Getenv("ADAPTER_INTERNAL_URL"), os.Getenv("NX_INTERNAL_KEY")),
		NNAdapter:  nnAdapter(),
		WFC:        wfc.New(os.Getenv("WFC_API_URL"), os.Getenv("WFC_API_SECRET")),
		Auth:       ratelimit.New(10, time.Minute),
		PerToken:   ratelimit.New(120, time.Minute),
		MediaLimit: ratelimit.New(5, time.Hour),
		log:        log,
	}
}

// RegisterRoutes mounts the API under the given path prefix.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	h := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, h) }
	g := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET /api/v1"+pattern, h) }
	d := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("DELETE /api/v1"+pattern, h) }

	h("/token", s.handleMintToken)              // POST /api/v1/token
	h("/token/2fa", s.handleMintToken2FA)       // POST /api/v1/token/2fa
	d("/token", s.auth(s.handleRevokeToken))    // DELETE /api/v1/token
	g("/me", s.auth(s.handleMe))                // GET /api/v1/me
	d("/me", s.auth(s.handleDeleteAccount))     // DELETE /api/v1/me {"password"}
	g("/me/sessions", s.auth(s.handleSessions)) // GET /api/v1/me/sessions
	d("/me/sessions/{id}", s.auth(s.handleRevokeSession))
	g("/me/friends", s.auth(s.handleFriends)) // GET /api/v1/me/friends
	s.registerSaves(mux)
	s.registerChat(mux)
	s.registerEmulator(mux)
	g("/me/friends/requests", s.auth(s.handleRequests))
	h("/me/friends/requests", s.auth(s.handleSendRequest)) // POST: add by friend code
	h("/me/friends/requests/accept", s.auth(s.handleAccept))
	h("/me/friends/requests/decline", s.auth(s.handleDecline))
	h("/me/friends/remove", s.auth(s.handleRemove))
	h("/me/friends/block", s.auth(s.handleBlock))
	h("/me/friends/unblock", s.auth(s.handleUnblock))
	g("/me/invitations", s.auth(s.handleInvitations))
	h("/me/invitations/decline", s.auth(s.handleDeclineInvitation))
	h("/me/push", s.auth(s.handleRegisterPush))
	d("/me/push", s.auth(s.handleForgetPush))
	h("/me/profile", s.auth(s.handleUpdateProfile))
	h("/me/password", s.auth(s.handleChangePassword))
	h("/me/links/wfc", s.auth(s.handleLinkWFC))
	d("/me/links/{namespace}", s.auth(s.handleUnlink))
	// Public: signing up and asking for a reset mail are what someone without
	// an account, or without its password, does.
	h("/register", s.handleRegister)
	h("/password/forgot", s.handleForgotPassword)
	h("/password/reset", s.handleResetPassword)
	g("/avatars", s.auth(s.handleAvatarChoices))
	// Requests + votes: the same scoreboard the website shows, for the app's
	// Titles tab. GET is public (a bearer only personalises `voted`); voting
	// needs one.
	g("/requests", s.handleListRequests)
	// Public: the title catalogue as the website's /titles page lists it.
	g("/titles", s.handleTitles)
	h("/requests/{id}/vote", s.auth(s.handleVoteRequest))
	s.registerCatalog(mux)
	// Public: no bearer. A status endpoint that needed an account could not
	// be read by the thing most likely to want it — someone whose sign-in is
	// the part that is broken. Registered through the same helper as
	// everything else so the contract test sees it; a route added straight on
	// the mux would slip past that check unnoticed.
	g("/status", s.handleStatus)
	// The network profile: what OpenPak is and what a client should send it,
	// generated from the live Traefik routing. One conditional GET per launch,
	// the same conditional GET the console itself already does for BCAT.
	s.registerNetwork(mux)
	// The v2 bundle distribution (/api/v2/network/*) is not here: it is its
	// own service, cmd/openpak-registry, so the website never holds the
	// registry's signing key. See docs/registry.md.
	// Public: the region nodes a room may be served from. The status page, the
	// app, and the matchmaking servers all read this one list.
	g("/regions", s.handleRegions)
	// A node's minute-by-minute announce, with its own key as the bearer. The
	// core's gRPC stays internal; this is the one door in.
	h("/federation/announce", s.handleAnnounce)

	s.registerInternal(mux)
	h("/me/avatar", s.auth(s.handleChooseAvatar))
	s.registerSecurity(mux)
}

// writeJSON mirrors the core's response conventions.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers {"error": English message, "code": stable id}. Clients
// translate the code; the message is for logs and curl.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg, "code": errorCode(code, msg)})
}

// errorCodes names the messages a person can meet; everything else gets its
// status's code. A message changed here changes its code, so keep them paired.
var errorCodes = map[string]string{
	"rate limited":                                  "rate_limited",
	"too many changes; slow down":                   "rate_limited",
	"too many wrong codes; try again in 15 minutes": "two_factor_locked",
	"invalid credentials":                           "invalid_credentials",
	"wrong password":                                "wrong_password",
	"invalid code":                                  "wrong_code",
	"wrong code":                                    "wrong_code",
	"invalid code, or the challenge has expired; sign in again": "challenge_expired",
	"invalid or expired token":                                  "session_expired",
	"bearer token required":                                     "session_expired",
	"this account is suspended":                                 "account_suspended",
	"registrations are closed":                                  "registrations_closed",
	"the account service did not answer":                        "service_unavailable",
	"the account service is not answering; try again":           "service_unavailable",
	"cloud saves are not available on this deployment":          "saves_unavailable",
	"saves service error":                                       "saves_unavailable",
	"chat is not available on this deployment":                  "chat_unavailable",
	"chat service error":                                        "chat_unavailable",
	"avatars are not configured":                                "avatars_unavailable",
	"Wii/DS linking is not available":                           "wfc_unavailable",
	"could not reach the Wii/DS server":                         "wfc_unavailable",
	"no Wii/DS profile has that friend code":                    "wfc_not_found",
	"that console is linked to another account":                 "console_taken",
	"no account for that friend code":                           "friend_code_unknown",
	"cannot befriend yourself":                                  "friend_self",
	"request exists or relationship conflicts":                  "friend_conflict",
	"no pending request":                                        "no_pending_request",
	"you can only message friends":                              "chat_not_friends",
	"you cannot message this account":                           "chat_blocked",
	"this request is not votable":                               "not_votable",
	"upload a PNG or JPEG up to 4 MiB":                          "avatar_invalid",
	"upload a PNG or JPEG up to 4 MiB in the avatar field":      "avatar_invalid",
	"not an image we can store":                                 "avatar_invalid",
	"unknown avatar":                                            "avatar_invalid",
	"the reset link is invalid or has expired":                  "link_expired",
	"the confirmation link is invalid or has expired":           "link_expired",
	"no such version":                                           "not_found",
	"session not found":                                         "not_found",
}

var statusCodes = map[int]string{
	http.StatusBadRequest: "bad_request", http.StatusUnauthorized: "unauthorized",
	http.StatusForbidden: "forbidden", http.StatusNotFound: "not_found",
	http.StatusConflict: "conflict", http.StatusLengthRequired: "bad_request",
	http.StatusTooManyRequests: "rate_limited", http.StatusBadGateway: "service_unavailable",
	http.StatusServiceUnavailable: "service_unavailable",
}

func errorCode(status int, msg string) string {
	if c, ok := errorCodes[msg]; ok {
		return c
	}
	if c, ok := statusCodes[status]; ok {
		return c
	}
	return "internal"
}

type tokenRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handleMintToken: POST /api/v1/token {email,password} → {token,expires_at}.
func (s *Server) handleMintToken(w http.ResponseWriter, r *http.Request) {
	if !s.Auth.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	var req tokenRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Establish the core web session through the public HTTP contract. A 2FA
	// account answers with a challenge instead, which POST /token/2fa redeems.
	coreSession, err := s.Core.Login(r.Context(), req.Email, req.Password)
	var challenge *core.LoginChallenge
	if errors.As(err, &challenge) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"two_factor_required": true, "challenge": challenge.Challenge,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.mintBearer(w, r, coreSession)
}

// handleMintToken2FA: POST /api/v1/token/2fa {challenge, code | recovery_code}
// finishes a sign-in that POST /token answered with a challenge.
func (s *Server) handleMintToken2FA(w http.ResponseWriter, r *http.Request) {
	if !s.Auth.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	var req struct {
		Challenge    string `json:"challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Challenge == "" || (req.Code == "" && req.RecoveryCode == "") {
		writeError(w, http.StatusBadRequest, "challenge and a code or recovery_code are required")
		return
	}
	coreSession, err := s.Core.Login2FA(r.Context(), req.Challenge, strings.TrimSpace(req.Code), strings.TrimSpace(req.RecoveryCode))
	switch {
	case err == nil:
		s.mintBearer(w, r, coreSession)
	case errors.Is(err, core.ErrInvalidCode):
		writeError(w, http.StatusUnauthorized, "invalid code, or the challenge has expired; sign in again")
	case errors.Is(err, core.ErrTwoFactorLocked):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many wrong codes; try again in 15 minutes")
	default:
		s.log.Error("mint token: 2fa", "err", err)
		writeError(w, http.StatusBadGateway, "the account service did not answer")
	}
}

// mintBearer binds a new bearer to a fresh core session. Whoami (CR-4) names
// the account; the password is never checked a second time.
func (s *Server) mintBearer(w http.ResponseWriter, r *http.Request, coreSession string) {
	acct, err := s.Core.Whoami(r.Context(), coreSession)
	if err != nil {
		_ = s.Core.Logout(context.Background(), coreSession)
		s.log.Error("mint token: whoami", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if acct.Status == "banned" {
		_ = s.Core.Logout(context.Background(), coreSession)
		writeError(w, http.StatusForbidden, "this account is suspended")
		return
	}
	raw, err := session.NewBearer()
	if err != nil {
		_ = s.Core.Logout(context.Background(), coreSession)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	id := newID()
	expires := time.Now().Add(TokenTTL)
	if err := s.DB.CreateAPIToken(r.Context(), id, s.Sessions.HashToken(raw), acct.ID, coreSession, expires); err != nil {
		_ = s.Core.Logout(context.Background(), coreSession)
		s.log.Error("create api token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      raw,
		"token_type": "Bearer",
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

// auth validates the bearer and its bound core session.
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, store.APIToken)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := session.BearerFromRequest(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "bearer token required")
			return
		}
		if !s.PerToken.Allow(raw[:min(8, len(raw))]) {
			writeError(w, http.StatusTooManyRequests, "rate limited")
			return
		}
		tok, err := s.DB.APITokenByHash(r.Context(), s.Sessions.HashToken(raw))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		// Re-validate the bound core session: revocation (logout, ban,
		// password reset) ends the bearer immediately.
		//
		// Only the core actually saying "that session is gone" may revoke.
		// A transport error or a 5xx means the core could not answer, and
		// revoking on that signs a phone out permanently for a restart it
		// never saw — which is exactly what happened on 2026-09-08.
		if _, err := s.Core.ListSessions(r.Context(), tok.CoreSessionID); err != nil {
			if !errors.Is(err, core.ErrSessionInvalid) {
				s.log.Error("api: core unreachable while validating a bearer", "err", err)
				writeError(w, http.StatusServiceUnavailable, "the account service is not answering; try again")
				return
			}
			_ = s.DB.RevokeAPIToken(r.Context(), tok.ID)
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		_ = s.DB.TouchAPIToken(r.Context(), tok.ID)
		next(w, r, tok)
	}
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if err := s.DB.RevokeAPIToken(r.Context(), tok.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Ending mobile access ends the core session too.
	_ = s.Core.Logout(r.Context(), tok.CoreSessionID)
	writeJSON(w, http.StatusOK, map[string]string{"message": "token revoked"})
}

// handleMe: GET /api/v1/me → account + friend code projection.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	acct, err := s.Identity.GetAccount(r.Context(), tok.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := map[string]any{
		"account_id":   tok.AccountID,
		"display_name": acct.DisplayName,
		"country":      acct.Country,
		"birthday":     acct.Birthday,
		// So the app can offer "resend the link" and show whether 2FA is on.
		"email_verified":     acct.EmailVerified,
		"two_factor_enabled": acct.TwoFactorEnabled,
		// The Identity contract does not carry the address; the session's whoami does.
		// Empty rather than an error if that call fails: the rest of /me still stands.
		"email": "",
		// Null when the account has never chosen one, so a client shows its own
		// fallback rather than a broken image. Relative to the site.
		"avatar_url": nil,
	}
	if me, err := s.Core.Whoami(r.Context(), tok.CoreSessionID); err == nil {
		resp["email"] = me.Email
	}
	if s.Media != nil {
		// Always set: every account has a picture, its own or the preset its
		// id picks, and the URL serves both. Versioned from the media row the
		// avatar writes stamp, so clients do not keep showing the old picture
		// from cache after a change.
		url := "/media/avatars/" + tok.AccountID + "/256"
		if rec, err := s.DB.GetMedia(r.Context(), tok.AccountID, "avatar"); err == nil && !rec.UpdatedAt.IsZero() {
			url += "?v=" + strconv.FormatInt(rec.UpdatedAt.UnixNano(), 10)
		}
		resp["avatar_url"] = url
	}
	// Linked consoles, every family the account reaches. A Wii/DS console is
	// linked from the website by friend code (WFC has no login); its subject
	// is the console-level user id, and per-title friend codes mean there is
	// no single code to project here — the console being listed is the point.
	platforms := []map[string]string{}
	if link, err := s.Links.ActiveLinkByAccount(r.Context(), "switch", tok.AccountID); err == nil {
		// The link subject is the console's user id, not a friend code. The
		// code the adapter published on the link, else the adapter's own
		// answer; none rather than dressing up a hex id as one.
		if code := accounts.FriendCode(r.Context(), link, s.Adapter, tok.AccountID); code != "" {
			resp["friend_code"] = code
		}
		platforms = append(platforms, map[string]string{"namespace": "switch", "link_id": link.LinkID})
	}
	if link, err := s.Links.ActiveLinkByAccount(r.Context(), "wfc", tok.AccountID); err == nil {
		platforms = append(platforms, map[string]string{"namespace": "wfc", "link_id": link.LinkID})
	}
	resp["linked_platforms"] = platforms
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	sessions, err := s.Core.ListSessions(r.Context(), tok.CoreSessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// core.Session carries no json tags, so marshalling it directly served
	// Go field names (ID, CreatedAt) and every client read an empty session.
	out := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, map[string]any{
			"id":         sess.ID,
			"created_at": sess.CreatedAt,
			"expires_at": sess.ExpiresAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	id := r.PathValue("id")
	if err := s.Core.RevokeSession(r.Context(), tok.CoreSessionID, id); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if id == tok.CoreSessionID {
		_ = s.DB.RevokeAPIToken(r.Context(), tok.ID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "session revoked"})
}

func (s *Server) handleFriends(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	friends, err := s.Social.ListFriends(r.Context(), tok.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	ids := make([]string, 0, len(friends))
	for _, f := range friends {
		ids = append(ids, f.AccountID)
	}
	// Presence is best-effort: a friends list that loads without it beats an
	// error page, so a presence failure is logged and the list still renders.
	presence, err := s.Invites.Presence(r.Context(), ids)
	if err != nil {
		s.log.Error("presence", "err", err)
	}
	// Built once per list, not once per friend.
	var catalogue map[string]string
	if len(presence) > 0 {
		catalogue = pages.TitleCatalogue(r.Context(), s.DB)
	}
	out := make([]map[string]any, 0, len(friends))
	for _, f := range friends {
		row := map[string]any{"account_id": f.AccountID, "display_name": f.DisplayName, "online": false}
		if p, ok := presence[f.AccountID]; ok {
			row["online"] = true
			pj := s.presenceJSON(r.Context(), catalogue, p)
			// Flat on the row, as the fields always were, and as one object
			// for clients that want presence in one place.
			for k, v := range pj {
				row[k] = v
			}
			row["presence"] = pj
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"friends": out})
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	in, err := s.Social.ListIncoming(r.Context(), tok.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out, err := s.Social.ListOutgoing(r.Context(), tok.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"incoming": friendsJSON(in), "outgoing": friendsJSON(out),
	})
}

// handleSendRequest: POST /api/v1/me/friends/requests {"friend_code": "..."}.
func (s *Server) handleSendRequest(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		FriendCode string `json:"friend_code"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// The core's "switch" link subject is the BAAS user id, not the friend
	// code: the adapter mints codes and publishes them on the link. So the
	// published code first, the adapter while codes are not published yet, and
	// the subject last, for someone who pastes that verbatim.
	target := accounts.AccountByFriendCode(r.Context(), s.Links, s.Adapter, req.FriendCode, normalizeCode(req.FriendCode))
	if target == "" {
		writeError(w, http.StatusNotFound, "no account for that friend code")
		return
	}
	if target == tok.AccountID {
		writeError(w, http.StatusBadRequest, "cannot befriend yourself")
		return
	}
	if err := s.Social.RequestFriend(r.Context(), tok.AccountID, target); err != nil {
		if errors.Is(err, core.ErrConflict) {
			writeError(w, http.StatusConflict, "request exists or relationship conflicts")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"message": "friend request sent", "account_id": target})
}

func (s *Server) handleAccept(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if err := s.Social.AcceptFriend(r.Context(), tok.AccountID, req.AccountID); err != nil {
		writeError(w, http.StatusConflict, "no pending request")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "friend added"})
}

// handleDecline: POST /api/v1/me/friends/requests/decline {"account_id"}. As on the website,
// declining removes the pending row; /me/friends/remove does the same, and this is the name a
// client looking for "decline" finds.
func (s *Server) handleDecline(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if err := s.Social.RemoveFriend(r.Context(), tok.AccountID, req.AccountID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "declined"})
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if err := s.Social.RemoveFriend(r.Context(), tok.AccountID, req.AccountID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "removed"})
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if err := s.Social.Block(r.Context(), tok.AccountID, req.AccountID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "blocked"})
}

func (s *Server) handleUnblock(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if err := s.Social.Unblock(r.Context(), tok.AccountID, req.AccountID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "unblocked"})
}

func decodeSmall(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func friendsJSON(fs []core.Friend) []map[string]string {
	out := make([]map[string]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]string{"account_id": f.AccountID, "display_name": f.DisplayName})
	}
	return out
}

// normalizeCode strips SW- display formatting (12 subject digits expected).
func normalizeCode(code string) string {
	out := make([]rune, 0, len(code))
	for _, c := range code {
		if c != '-' && c != ' ' {
			out = append(out, c)
		}
	}
	s := string(out)
	if len(s) == 14 && (s[:2] == "SW" || s[:2] == "sw") {
		s = s[2:]
	}
	return s
}

// friendCode formats a link subject for display (SW-xxxx-xxxx-xxxx when 16
// hex digits; verbatim otherwise). Mirrors the web page formatting.
func friendCode(subject string) string {
	var hex []rune
	for _, c := range subject {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			hex = append(hex, c)
		case c == '-':
		default:
			return subject
		}
	}
	if len(hex) != 12 {
		return subject
	}
	return "SW-" + string(hex[0:4]) + "-" + string(hex[4:8]) + "-" + string(hex[8:12])
}

func clientKey(r *http.Request) string {
	host := r.RemoteAddr
	if i := len(host) - 1; i > 0 {
		for i >= 0 && host[i] != ':' {
			i--
		}
		if i > 0 {
			return host[:i]
		}
	}
	return host
}

// newID mints a random token id for the store (the raw bearer is hashed;
// the id is only for revocation).
func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// handleInvitations is the phone's inbox: what is waiting, who sent it, and
// when it lapses. It never consumes anything — delivery to the console is a
// separate path and reading this must not interfere with it.
func (s *Server) handleInvitations(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	ins, err := s.Invites.ListPending(r.Context(), tok.AccountID)
	if err != nil {
		s.log.Error("list invitations", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(ins))
	for _, in := range ins {
		out = append(out, map[string]any{
			"invitation_id": in.ID,
			"from":          in.FromName,
			"title_id":      in.TitleID,
			"namespace":     in.Namespace,
			"expires_at":    in.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": out})
}

// handleDeclineInvitation is the decline button. Declining something already
// gone is not an error: the phone that asked twice, or raced the console, gets
// the same calm answer.
func (s *Server) handleDeclineInvitation(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		InvitationID string `json:"invitation_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.InvitationID == "" {
		writeError(w, http.StatusBadRequest, "invitation_id is required")
		return
	}
	declined, err := s.Invites.Decline(r.Context(), tok.AccountID, req.InvitationID)
	if err != nil {
		s.log.Error("decline invitation", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"declined": declined})
}

// handleRegisterPush records the device token this install can be reached at.
// Keyed by the token, so signing in as someone else on the same phone moves it
// rather than leaving the previous account's notifications arriving here.
func (s *Server) handleRegisterPush(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	if req.Platform == "" {
		req.Platform = "fcm"
	}
	if err := s.DB.SavePushToken(r.Context(), tok.AccountID, req.Token, req.Platform); err != nil {
		s.log.Error("save push token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"registered": true})
}

// handleForgetPush drops a device token: sign-out, or notifications turned off.
func (s *Server) handleForgetPush(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	if err := s.DB.DeletePushToken(r.Context(), token); err != nil {
		s.log.Error("delete push token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forgotten": true})
}

// handleUpdateProfile edits the account. Every field is optional and an empty
// one is left alone, so a screen that edits a single thing sends a single
// thing rather than having to echo back the rest.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	var req struct {
		DisplayName string `json:"display_name"`
		Country     string `json:"country"`
		Birthday    string `json:"birthday"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// The core keeps a blank display name but stores country and birthday as sent, so a
	// field the app left out is filled from the account before it can be erased.
	if req.Country == "" || req.Birthday == "" {
		acct, err := s.Identity.GetAccount(r.Context(), tok.AccountID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "could not read the account")
			return
		}
		if req.Country == "" {
			req.Country = acct.Country
		}
		if req.Birthday == "" {
			req.Birthday = acct.Birthday
		}
	}
	err := s.Core.UpdateProfile(r.Context(), tok.CoreSessionID, req.Country, req.Birthday, req.DisplayName)
	if err != nil {
		// The core validates: a two-letter country, an ISO date, a name of a
		// sane length. Its complaint is the useful one to pass on.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// handleAvatarChoices lists the built-in avatars, so the app can offer the
// same set as the website instead of hardcoding names that would drift.
func (s *Server) handleAvatarChoices(w http.ResponseWriter, r *http.Request, _ store.APIToken) {
	out := make([]map[string]string, 0, len(media.PresetIDs))
	for _, id := range media.PresetIDs {
		out = append(out, map[string]string{"id": id, "url": "/media/avatars/preset/" + id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"avatars": out})
}

// handleChooseAvatar sets one of the built-ins from JSON {"preset"}, or stores
// an uploaded photo sent as multipart/form-data.
func (s *Server) handleChooseAvatar(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.Media == nil {
		writeError(w, http.StatusServiceUnavailable, "avatars are not configured")
		return
	}
	if !s.MediaLimit.Allow(tok.AccountID) {
		writeError(w, http.StatusTooManyRequests, "too many changes; slow down")
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.uploadAvatar(w, r, tok)
		return
	}
	var req struct {
		Preset string `json:"preset"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Preset == "" {
		writeError(w, http.StatusBadRequest, "preset is required")
		return
	}
	if err := s.Media.StorePreset(r.Context(), tok.AccountID, req.Preset); err != nil {
		if errors.Is(err, media.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "unknown avatar")
			return
		}
		s.log.Error("store preset avatar", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.stampAvatar(r, tok.AccountID)
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// handleListRequests: GET /api/v1/requests?kind=title|feature → every votable
// item with its vote count. Auth is optional: `voted` is false without a bearer
// and reflects the caller's own votes with one. The app fetches all and
// filters/sorts client-side, like the web /titles page.
func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind") // "" = all
	reqs, err := s.DB.ListRequests(r.Context(), kind, s.optionalAccount(r))
	if err != nil {
		s.log.Error("api: list requests", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items := make([]map[string]any, 0, len(reqs))
	for _, q := range reqs {
		m := requestJSON(q)
		m["votes"], m["voted"] = q.Votes, q.Voted
		items = append(items, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleTitles: GET /api/v1/titles → the title catalogue in the order the website's /titles
// page shows it, each with its status. `requestable` marks the long tail the page keeps behind
// "Request a game"; votes stay on /requests.
func (s *Server) handleTitles(w http.ResponseWriter, r *http.Request) {
	// The per-token budget, keyed by address instead: a catalogue read must not spend the
	// sign-in budget that s.Auth keeps for the same address.
	if !s.PerToken.Allow("ip:" + clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	reqs, err := s.DB.ListRequests(r.Context(), "title", "")
	if err != nil {
		s.log.Error("api: list titles", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	titles := make([]map[string]any, 0, len(reqs))
	for _, t := range store.CatalogOrder(reqs) {
		titles = append(titles, map[string]any{
			"id": t.ID, "name": t.Name, "console": t.Console, "category": t.Category, "title_id": t.TitleID,
			"status": t.Status, "notes": t.Notes, "requestable": t.Votable(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": titles})
}

// handleVoteRequest: POST /api/v1/requests/{id}/vote → toggles the caller's vote
// and returns the fresh count. Idempotent: calling again removes the vote.
func (s *Server) handleVoteRequest(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return
	}
	if _, err := s.DB.ToggleVote(r.Context(), id, tok.AccountID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotVotable):
			writeError(w, http.StatusConflict, "this request is not votable")
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusBadRequest, "no such request")
		default:
			s.log.Error("api: toggle vote", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	votes, voted, err := s.DB.RequestVotes(r.Context(), id, tok.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "votes": votes, "voted": voted})
}

// optionalAccount resolves a bearer if one is present and valid, else "". Unlike
// s.auth it never rejects: an absent or stale token just means an anonymous read.
func (s *Server) optionalAccount(r *http.Request) string {
	raw := session.BearerFromRequest(r)
	if raw == "" {
		return ""
	}
	tok, err := s.DB.APITokenByHash(r.Context(), s.Sessions.HashToken(raw))
	if err != nil {
		return ""
	}
	return tok.AccountID
}

// handleStatus is the machine-readable half of the status page: who is online,
// per title and per console network. Public on purpose — the people most likely
// to check it are the ones who cannot sign in.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	titles, networks, total, err := s.Invites.PlayerCounts(r.Context())
	if err != nil {
		s.log.Error("status: player counts", "err", err)
		writeError(w, http.StatusBadGateway, "counts unavailable")
		return
	}
	rows := make([]map[string]any, 0, len(titles))
	for _, t := range titles {
		rows = append(rows, map[string]any{
			"title_id": t.TitleID, "namespace": t.Namespace, "players": t.Players,
		})
	}
	nets := make([]map[string]any, 0, len(networks))
	for ns, n := range networks {
		nets = append(nets, map[string]any{"namespace": ns, "players": n})
	}
	sort.Slice(nets, func(i, j int) bool {
		return nets[i]["namespace"].(string) < nets[j]["namespace"].(string)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"players_online": total,
		"titles":         rows,
		"networks":       nets,
	})
}

func nodeJSON(n core.Node) map[string]any {
	var seen int64
	if !n.LastSeen.IsZero() {
		seen = n.LastSeen.Unix()
	}
	return map[string]any{"id": n.ID, "name": n.Name, "country": n.Country, "services": n.Services, "players": n.Players, "last_seen_unix": seen}
}

// handleRegions is cached for a moment because every matchmaking server polls
// it and the answer changes once a minute at most.
func (s *Server) handleRegions(w http.ResponseWriter, r *http.Request) {
	if s.Federation == nil {
		writeError(w, http.StatusServiceUnavailable, "federation not configured")
		return
	}
	nodes, err := s.Federation.ListNodes(r.Context())
	if err != nil {
		s.log.Error("regions", "err", err)
		writeError(w, http.StatusBadGateway, "regions unavailable")
		return
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeJSON(n))
	}
	w.Header().Set("Cache-Control", "public, max-age=15")
	writeJSON(w, http.StatusOK, map[string]any{"regions": out})
}

func (s *Server) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	if s.Federation == nil {
		writeError(w, http.StatusServiceUnavailable, "federation not configured")
		return
	}
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(key, "opnode_") {
		writeError(w, http.StatusUnauthorized, "node key required")
		return
	}
	var in struct {
		Services map[string]string `json:"services"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id, approved, err := s.Federation.AnnounceAs(r.Context(), key, in.Services)
	if err != nil {
		if strings.Contains(err.Error(), "credentials rejected") {
			writeError(w, http.StatusUnauthorized, "unknown node key")
			return
		}
		s.log.Error("announce", "err", err)
		writeError(w, http.StatusBadGateway, "announce failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"node_id": id, "approved": approved})
}

// nnAdapter is the Wii U/3DS adapter client, or nil until NN_ACCOUNT_INTERNAL_URL is set.
func nnAdapter() *adapter.Client {
	if u := os.Getenv("NN_ACCOUNT_INTERNAL_URL"); u != "" {
		return adapter.New(u, os.Getenv("NN_ACCOUNT_INTERNAL_KEY"))
	}
	return nil
}

// presenceJSON is one friend's live presence as the API answers it anywhere:
// where (namespace/platform), on what (client, and label to show it), and
// what (title_id, and title_name when any catalogue knows it). A phone reads
// "<title_name> on <label>", or "Online on <label>" when there is no title.
func (s *Server) presenceJSON(ctx context.Context, catalogue map[string]string, p core.Presence) map[string]any {
	name, _ := pages.FriendGame(ctx, catalogue, p.Namespace, p.TitleID)
	return map[string]any{
		"title_id":   p.TitleID,
		"namespace":  p.Namespace,
		"since":      p.Since.UTC().Format(time.RFC3339),
		"platform":   p.Namespace,
		"client":     p.Client,
		"label":      p.Label(),
		"title_name": name,
	}
}
