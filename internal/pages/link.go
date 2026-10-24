package pages

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/xbl"
)

// /link is where a person approves the code a game shows for a Microsoft-style
// device sign-in (Minecraft Dungeons first): the game says "go to openpak.org/link
// and enter ABCD2345". The site's own session is the proof of who is approving
// (password and two-factor were asked at sign-in); xbl's private API does the rest.
//
//	GET  /link[?code=|?otc=]   signed out: sign-in prompt that returns here; signed in: the code form
//	POST /link                 look the code up, show "Link <title> to <you>?"
//	POST /link/approve         approve it; the game's next poll signs it in

// linkTitles names the games behind an MSA client id; unknown ids read as "a game".
var linkTitles = map[string]string{
	"000000004426B87D": "Minecraft Dungeons",
}

type linkView struct {
	baseData
	// Step is signin, enter, confirm or done.
	Step      string
	Code      string
	LoginHref string
	Error     string
	ClientID  string
	TitleName string
	Handle    string
	Gamertag  string
	Expires   string
	// Question and Done are the confirm and success sentences, names filled in.
	Question string
	Done     string
}

// linkCode takes a code as typed or as the game sent it: case, spaces and dashes
// do not matter, and anything that cannot be a code is dropped.
func linkCode(raw string) string {
	c := xbl.NormalizeCode(raw)
	if len(c) == 0 || len(c) > 16 {
		return ""
	}
	for _, r := range c {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return c
}

// linkNext is the only return address the sign-in pages accept: /link with an
// optional, well-formed code. Anything else (other paths, hosts) is dropped.
func linkNext(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path != "/link" {
		return ""
	}
	if c := linkCode(u.Query().Get("code")); c != "" {
		return "/link?code=" + c
	}
	return "/link"
}

func (s *Server) linkTitle(lang, clientID string) string {
	if n, ok := linkTitles[clientID]; ok {
		return n
	}
	return i18n.T(lang, "link.title.unknown")
}

func (s *Server) handleLinkForm(w http.ResponseWriter, r *http.Request) {
	lang := i18n.FromRequest(r)
	q := r.URL.Query()
	code := linkCode(q.Get("code"))
	if code == "" {
		code = linkCode(q.Get("otc")) // Microsoft's link URLs carry it as otc
	}
	v := s.viewerOf(w, r)
	view := linkView{baseData: s.data(r, v, i18n.T(lang, "link.title")), Code: code, Step: "enter"}
	if v == nil {
		next := "/link"
		if code != "" {
			next += "?code=" + code
		}
		view.Step, view.LoginHref = "signin", "/login?next="+url.QueryEscape(next)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, http.StatusOK, "link.html", view)
}

// linkAllowed spends one code attempt for the account and for the address.
func (s *Server) linkAllowed(r *http.Request, accountID string) bool {
	if s.LinkLimit == nil {
		return true
	}
	return s.LinkLimit.Allow("acct:"+accountID) && s.LinkLimit.Allow("ip:"+clientKey(r))
}

// linkFail shows the code form again with what went wrong.
func (s *Server) linkFail(w http.ResponseWriter, r *http.Request, v *viewer, code string, err error) {
	lang := i18n.FromRequest(r)
	status, key := http.StatusServiceUnavailable, "link.err.unavailable"
	switch {
	case errors.Is(err, errLinkEmpty):
		status, key = http.StatusBadRequest, "link.err.empty"
	case errors.Is(err, errLinkSlow):
		status, key = http.StatusTooManyRequests, "link.err.slow"
	case errors.Is(err, xbl.ErrUnknown):
		status, key = http.StatusNotFound, "link.err.unknown"
	case errors.Is(err, xbl.ErrExpired):
		status, key = http.StatusGone, "link.err.expired"
	case errors.Is(err, xbl.ErrUsed):
		status, key = http.StatusConflict, "link.err.used"
	case errors.Is(err, xbl.ErrAccount):
		status, key = http.StatusForbidden, "link.err.account"
	default:
		s.log.Error("link: xbl", "err", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, status, "link.html", linkView{baseData: s.data(r, v, i18n.T(lang, "link.title")),
		Step: "enter", Code: code, Error: i18n.T(lang, key)})
}

var (
	errLinkEmpty = errors.New("no code")
	errLinkSlow  = errors.New("too many attempts")
)

func (s *Server) handleLinkCheck(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	code := linkCode(r.PostFormValue("code"))
	if code == "" {
		s.linkFail(w, r, v, xbl.NormalizeCode(r.PostFormValue("code")), errLinkEmpty)
		return
	}
	if !s.linkAllowed(r, v.AccountID) {
		s.linkFail(w, r, v, code, errLinkSlow)
		return
	}
	p, err := s.XBL.Lookup(r.Context(), code)
	if err != nil {
		s.linkFail(w, r, v, code, err)
		return
	}
	lang := i18n.FromRequest(r)
	view := linkView{baseData: s.data(r, v, i18n.T(lang, "link.title")), Step: "confirm", Code: code,
		ClientID: p.ClientID, TitleName: s.linkTitle(lang, p.ClientID), Handle: v.DisplayName}
	if view.Handle == "" {
		view.Handle = v.Email
	}
	view.Question = strings.NewReplacer("{title}", view.TitleName, "{account}", view.Handle).Replace(i18n.T(lang, "link.confirm.q"))
	if !p.ExpiresAt.IsZero() {
		view.Expires = p.ExpiresAt.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, http.StatusOK, "link.html", view)
}

func (s *Server) handleLinkApprove(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	code := linkCode(r.PostFormValue("code"))
	if code == "" {
		s.linkFail(w, r, v, "", errLinkEmpty)
		return
	}
	if !s.linkAllowed(r, v.AccountID) {
		s.linkFail(w, r, v, code, errLinkSlow)
		return
	}
	gamertag, err := s.XBL.Approve(r.Context(), code, v.AccountID)
	if err != nil {
		s.linkFail(w, r, v, code, err)
		return
	}
	s.log.Info("link: device code approved", "account", v.AccountID)
	lang := i18n.FromRequest(r)
	clientID := r.PostFormValue("client_id")
	w.Header().Set("Cache-Control", "no-store")
	title := s.linkTitle(lang, clientID)
	if gamertag == "" {
		gamertag = v.DisplayName
	}
	s.render(w, http.StatusOK, "link.html", linkView{baseData: s.data(r, v, i18n.T(lang, "link.title")),
		Step: "done", TitleName: title, Gamertag: gamertag,
		Done: strings.NewReplacer("{title}", title, "{gamertag}", gamertag).Replace(i18n.T(lang, "link.done"))})
}
