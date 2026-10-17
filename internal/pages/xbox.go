package pages

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/openpak/website/internal/xbaccount"
)

// xboxGamertag is the signed-in account's original-Xbox gamertag: the public code of its
// "xbox" link in core, which xb-account keeps current. Empty when the identity does not exist
// yet (xb-account makes it within seconds of a new account) or core cannot say.
func (s *Server) xboxGamertag(ctx context.Context, accountID string) string {
	link, err := s.Links.ActiveLinkByAccount(ctx, "xbox", accountID)
	if err != nil {
		return ""
	}
	return link.PublicCode
}

// newRequestID names one rendering of the form. Submitting that rendering twice (double click,
// reload of the POST) is one change to xb-account; a new rendering is a new request.
func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "web-" + hex.EncodeToString(b)
}

var requestIDRe = regexp.MustCompile(`^web-[0-9a-f]{32}$`)

// handleXboxGamertag changes the gamertag. The account comes from the session, never the form;
// the rules are xb-account's, which answers each refusal with its own code.
func (s *Server) handleXboxGamertag(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if s.XB == nil {
		http.Error(w, "Xbox gamertags are not available", http.StatusServiceUnavailable)
		return
	}
	tag := strings.TrimSpace(r.PostFormValue("gamertag"))
	if tag == "" || utf8.RuneCountInString(tag) > 64 {
		redirectMsg(w, r, "/account", "xbox-invalid")
		return
	}
	reqID := r.PostFormValue("request_id")
	if !requestIDRe.MatchString(reqID) {
		reqID = newRequestID()
	}
	res, err := s.XB.SetGamertag(r.Context(), v.AccountID, tag, reqID, "website:"+v.AccountID)
	msg := ""
	switch {
	case err == nil && res.Changed:
		msg = "xbox-saved"
	case err == nil:
		msg = "xbox-same"
	case errors.Is(err, xbaccount.ErrInvalid):
		msg = "xbox-invalid"
	case errors.Is(err, xbaccount.ErrReserved):
		msg = "xbox-reserved"
	case errors.Is(err, xbaccount.ErrTaken):
		msg = "xbox-taken"
	case errors.Is(err, xbaccount.ErrConsoleBound):
		msg = "xbox-console-bound"
	case errors.Is(err, xbaccount.ErrRequestReused):
		msg = "xbox-stale"
	case errors.Is(err, xbaccount.ErrNoIdentity):
		msg = "xbox-noidentity"
	default:
		s.log.Error("xbox gamertag", "err", err)
		msg = "xbox-unavailable"
	}
	redirectMsg(w, r, "/account", msg)
}
