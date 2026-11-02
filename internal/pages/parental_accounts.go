package pages

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/supervision"
)

// Account-level parental controls on /account/parental: who supervises whom, and the
// parent-controlled OpenVerse level ("off", "read", "read + post") of each supervised
// account. The data lives in the parental controls core; this page is its web form.

type acctPerson struct{ Name, Handle string }

type supRow struct {
	MemberID, SupervisorID string
	Who                    acctPerson // the other party
	Role                   string
}

type childRow struct {
	supRow
	Active  bool
	Level   string
	Options []option
}

// accountsView is the "Supervised accounts" section. Nil hides it (core not configured).
type accountsView struct {
	Unavailable bool
	Invites     []supRow // asked of this account: accept or decline
	Parents     []supRow // active supervisors of this account
	Pending     []supRow // invitations this account sent that are not accepted yet
	Children    []childRow
	Notice      string // translated, from the redirect
	Error       string
}

func (s *Server) person(ctx context.Context, id string) acctPerson {
	if s.Identity == nil {
		return acctPerson{}
	}
	a, err := s.Identity.GetAccount(ctx, id)
	if err != nil {
		return acctPerson{}
	}
	return acctPerson{Name: a.DisplayName, Handle: a.Handle}
}

func levelOptions(lang, current string) []option {
	var out []option
	for _, l := range supervision.Levels {
		out = append(out, option{Value: l, Label: i18n.T(lang, "parental.acct.level."+l), Selected: l == current})
	}
	return out
}

func (s *Server) accountsSection(ctx context.Context, r *http.Request, v *viewer) *accountsView {
	if s.Supervision == nil {
		return nil
	}
	lang := i18n.FromRequest(r)
	av := &accountsView{}
	q := r.URL.Query()
	switch n := q.Get("sup"); n {
	case "invited", "linked", "accepted", "removed", "saved":
		av.Notice = i18n.T(lang, "parental.acct.done."+n)
	}
	switch e := q.Get("superr"); e {
	case "":
	case "notfound", "conflict", "invalid", "unavailable":
		av.Error = i18n.T(lang, "parental.acct.err."+e)
	default:
		av.Error = i18n.T(lang, "parental.acct.err.failed")
	}
	mine, err := s.Supervision.Mine(ctx, v.CoreSessionID)
	if err != nil {
		s.log.Error("parental accounts", "err", err)
		av.Unavailable = true
		return av
	}
	for _, row := range mine.SupervisedBy { // I am the member
		sr := supRow{MemberID: row.MemberID, SupervisorID: row.SupervisorID, Role: row.Role, Who: s.person(ctx, row.SupervisorID)}
		if row.State == "active" {
			av.Parents = append(av.Parents, sr)
		} else if row.Role == "guardian" {
			// A guardian invite is answered by the guardian, not the member.
			av.Pending = append(av.Pending, sr)
		} else {
			av.Invites = append(av.Invites, sr)
		}
	}
	for _, row := range mine.Supervising { // I am the supervisor
		sr := supRow{MemberID: row.MemberID, SupervisorID: row.SupervisorID, Role: row.Role, Who: s.person(ctx, row.MemberID)}
		if row.State != "active" {
			if row.Role == "guardian" {
				av.Invites = append(av.Invites, sr)
			} else {
				av.Pending = append(av.Pending, sr)
			}
			continue
		}
		level, _, err := s.Supervision.OpenVerseLevel(ctx, v.CoreSessionID, row.MemberID)
		if err != nil {
			s.log.Error("parental openverse level", "err", err)
			level = supervision.LevelOff
		}
		av.Children = append(av.Children, childRow{supRow: sr, Active: true, Level: level, Options: levelOptions(lang, level)})
	}
	return av
}

func accountsRedirect(w http.ResponseWriter, r *http.Request, key, val string) {
	http.Redirect(w, r, "/account/parental?"+key+"="+url.QueryEscape(val)+"#accounts", http.StatusSeeOther)
}

func accountsErr(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	code := "failed"
	var se *supervision.Error
	switch {
	case errors.As(err, &se) && se.Status == http.StatusNotFound:
		code = "notfound"
	case errors.As(err, &se) && se.Status == http.StatusConflict:
		code = "conflict"
	case errors.As(err, &se) && se.Status == http.StatusBadRequest:
		code = "invalid"
	case errors.Is(err, supervision.ErrUnavailable):
		code = "unavailable"
	}
	if code == "failed" {
		s.log.Error("parental accounts write", "err", err)
	}
	accountsRedirect(w, r, "superr", code)
}

func (s *Server) handleSupervisionInvite(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok || s.Supervision == nil {
		return
	}
	typed := strings.TrimPrefix(strings.TrimSpace(r.PostFormValue("handle")), "@")
	handles, isHandles := s.Identity.(core.Handles)
	if typed == "" || !isHandles {
		accountsRedirect(w, r, "superr", "notfound")
		return
	}
	id, err := handles.ResolveHandle(r.Context(), typed)
	if err != nil || id == "" {
		accountsRedirect(w, r, "superr", "notfound")
		return
	}
	state, err := s.Supervision.Invite(r.Context(), v.CoreSessionID, id)
	if err != nil {
		accountsErr(w, r, s, err)
		return
	}
	if state == "active" {
		accountsRedirect(w, r, "sup", "linked")
		return
	}
	accountsRedirect(w, r, "sup", "invited")
}

func (s *Server) handleSupervisionAccept(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok || s.Supervision == nil {
		return
	}
	if err := s.Supervision.Accept(r.Context(), v.CoreSessionID, r.PostFormValue("member"), r.PostFormValue("supervisor")); err != nil {
		accountsErr(w, r, s, err)
		return
	}
	accountsRedirect(w, r, "sup", "accepted")
}

func (s *Server) handleSupervisionRemove(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok || s.Supervision == nil {
		return
	}
	if err := s.Supervision.Remove(r.Context(), v.CoreSessionID, r.PostFormValue("member"), r.PostFormValue("supervisor")); err != nil {
		accountsErr(w, r, s, err)
		return
	}
	accountsRedirect(w, r, "sup", "removed")
}

func (s *Server) handleSupervisionOpenVerse(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok || s.Supervision == nil {
		return
	}
	level := r.PostFormValue("level")
	if !supervision.ValidLevel(level) {
		accountsRedirect(w, r, "superr", "invalid")
		return
	}
	if err := s.Supervision.SetOpenVerseLevel(r.Context(), v.CoreSessionID, r.PostFormValue("member"), level); err != nil {
		accountsErr(w, r, s, err)
		return
	}
	accountsRedirect(w, r, "sup", "saved")
}
