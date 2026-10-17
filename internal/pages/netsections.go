package pages

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/xbaccount"
)

// The Networks area of /account holds what only one family of networks cares about. Each
// family is one entry in networkSections: its title, its single editable field, and the
// backends that store the value. Shared things (display name, country, birthday, profile
// picture) stay in the common profile section above and are used on every network; no network
// keeps its own copy. Adding a network is adding an entry here and its i18n keys.

// netBackend is one service that stores a section's value.
type netBackend struct {
	ID      string // stable code used in redirects
	NameKey string // i18n key of the network's name
	// Apply writes the value for the account. It must be idempotent for a repeated requestID.
	// nil Apply or Enabled == false means the backend is skipped (not configured).
	Enabled func(*Server) bool
	Apply   func(ctx context.Context, s *Server, accountID, value, requestID, actor string) error
}

// netSection is one network family.
type netSection struct {
	ID       string
	TitleKey string
	HintKey  string
	// Field is the one editable value; Name is also the URL segment and the form field.
	Field struct {
		Name, LabelKey, RulesKey, SubmitKey string
		MaxLen                              int
		Pattern                             string // client-side hint only; the backends decide
	}
	// Current reads the value as stored; "" means the account has no identity there yet.
	Current func(ctx context.Context, s *Server, accountID string) string
	// Backends are applied in order. A failure stops the run: nothing after it is attempted.
	Backends []netBackend
	// Classify turns a backend error into a short why-code; its message is "account.xbox."+code
	// style keys named by WhyKey. Unknown errors are "unavailable".
	Classify func(error) string
	WhyKey   func(why string) string
	OKKey    string
	SameKey  string
}

// gamertagSetter is what an Xbox backend offers; xbaccount.Client is the original Xbox one.
type gamertagSetter interface {
	SetGamertag(ctx context.Context, coreAccountID, gamertag, requestID, actor string) (xbaccount.Result, error)
}

var networkSections = []netSection{xboxSection()}

func xboxSection() netSection {
	sec := netSection{ID: "xbox", TitleKey: "account.xbox.title", HintKey: "account.xbox.hint",
		OKKey: "account.xbox.ok", SameKey: "account.xbox.same",
		Current: func(ctx context.Context, s *Server, accountID string) string {
			// The gamertag is the public code of the account's core "xbox" link, which xb-account
			// keeps current; empty until the identity exists.
			link, err := s.Links.ActiveLinkByAccount(ctx, "xbox", accountID)
			if err != nil {
				return ""
			}
			return link.PublicCode
		},
		WhyKey: func(why string) string { return "account.xbox." + why },
		Classify: func(err error) string {
			switch {
			case errors.Is(err, xbaccount.ErrInvalid):
				return "invalid"
			case errors.Is(err, xbaccount.ErrReserved):
				return "reserved"
			case errors.Is(err, xbaccount.ErrTaken):
				return "taken"
			case errors.Is(err, xbaccount.ErrConsoleBound):
				return "consolebound"
			case errors.Is(err, xbaccount.ErrRequestReused):
				return "stale"
			case errors.Is(err, xbaccount.ErrNoIdentity):
				return "noidentity"
			}
			return "unavailable"
		}}
	sec.Field.Name, sec.Field.LabelKey, sec.Field.RulesKey, sec.Field.SubmitKey = "gamertag", "account.xbox.label", "account.xbox.rules", "account.xbox.submit"
	sec.Field.MaxLen, sec.Field.Pattern = 15, "[A-Za-z0-9]+( [A-Za-z0-9]+)*"
	// One gamertag for both Xbox networks. The original Xbox is first; the 360 target is
	// skipped until its backend exists (Server.X360 is nil), see docs in example.env.
	sec.Backends = []netBackend{
		{ID: "xbox", NameKey: "account.net.name.xbox",
			Enabled: func(s *Server) bool { return s.XB != nil },
			Apply: func(ctx context.Context, s *Server, id, v, rid, actor string) error {
				_, err := s.XB.SetGamertag(ctx, id, v, rid, actor)
				return err
			}},
		{ID: "x360", NameKey: "account.net.name.x360",
			Enabled: func(s *Server) bool { return s.X360 != nil },
			Apply: func(ctx context.Context, s *Server, id, v, rid, actor string) error {
				_, err := s.X360.SetGamertag(ctx, id, v, rid, actor)
				return err
			}},
	}
	return sec
}

// netView is one section as the template sees it.
type netView struct {
	ID, TitleKey, HintKey string
	FieldName, LabelKey   string
	RulesKey, SubmitKey   string
	MaxLen                int
	Pattern               string
	Current               string
	RequestID             string
	Targets               []string // i18n keys of the networks the edit applies to
}

func (s *Server) enabledBackends(sec *netSection) []netBackend {
	var out []netBackend
	for _, b := range sec.Backends {
		if b.Enabled(s) {
			out = append(out, b)
		}
	}
	return out
}

// networkViews builds the sections that have at least one configured backend. rid, when it has
// our shape, is reused so that a retry after a partial failure carries the same request id.
func (s *Server) networkViews(ctx context.Context, accountID, rid string) []netView {
	var out []netView
	for i := range networkSections {
		sec := &networkSections[i]
		be := s.enabledBackends(sec)
		if len(be) == 0 {
			continue
		}
		v := netView{ID: sec.ID, TitleKey: sec.TitleKey, HintKey: sec.HintKey, FieldName: sec.Field.Name,
			LabelKey: sec.Field.LabelKey, RulesKey: sec.Field.RulesKey, SubmitKey: sec.Field.SubmitKey,
			MaxLen: sec.Field.MaxLen, Pattern: sec.Field.Pattern, Current: sec.Current(ctx, s, accountID)}
		if requestIDRe.MatchString(rid) {
			v.RequestID = rid
		} else {
			v.RequestID = newRequestID()
		}
		for _, b := range be {
			v.Targets = append(v.Targets, b.NameKey)
		}
		out = append(out, v)
	}
	return out
}

// newRequestID names one rendering of a form. Submitting that rendering twice (double click,
// reload of the POST) is one change; a new rendering is a new request.
func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "web-" + hex.EncodeToString(b)
}

var requestIDRe = regexp.MustCompile(`^web-[0-9a-f]{32}$`)

// handleNetworkField applies a network section's field to each configured backend, in order.
//
// Partial failure: the first failing backend stops the run. If it was the first backend nothing
// changed anywhere. If an earlier backend had already applied, the user is told which networks
// changed and which did not, never "saved", and the redirect carries the request id so the
// form's retry reuses it: backends that already applied answer a replay without changing
// anything, and the failed one is tried again.
func (s *Server) handleNetworkField(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	var sec *netSection
	for i := range networkSections {
		if networkSections[i].ID == r.PathValue("network") && networkSections[i].Field.Name == r.PathValue("field") {
			sec = &networkSections[i]
		}
	}
	if sec == nil {
		http.NotFound(w, r)
		return
	}
	backends := s.enabledBackends(sec)
	if len(backends) == 0 {
		http.Error(w, "this network is not available", http.StatusServiceUnavailable)
		return
	}
	value := strings.TrimSpace(r.PostFormValue(sec.Field.Name))
	if value == "" || utf8.RuneCountInString(value) > 64 {
		redirectMsg(w, r, "/account", "net-failed:"+backends[0].ID+":invalid")
		return
	}
	reqID := r.PostFormValue("request_id")
	if !requestIDRe.MatchString(reqID) {
		reqID = newRequestID()
	}
	var done []string
	for _, b := range backends {
		// The account is the session's; the form names no account.
		if err := b.Apply(r.Context(), s, v.AccountID, value, reqID, "website:"+v.AccountID); err != nil {
			why := sec.Classify(err)
			if why == "unavailable" {
				s.log.Error("network field", "network", sec.ID, "backend", b.ID, "err", err)
			}
			code := "net-failed:" + b.ID + ":" + why
			if len(done) > 0 {
				code += ":" + strings.Join(done, ",")
				http.Redirect(w, r, "/account?m="+url.QueryEscape(code)+"&rid="+reqID, http.StatusSeeOther)
				return
			}
			redirectMsg(w, r, "/account", code)
			return
		}
		done = append(done, b.ID)
	}
	redirectMsg(w, r, "/account", "net-saved:"+sec.ID)
}

// networkFlash renders the "net-..." redirect codes; ok is false for other codes.
func networkFlash(lang, code string) (string, bool) {
	parts := strings.Split(code, ":")
	switch parts[0] {
	case "net-saved":
		if len(parts) == 2 {
			for i := range networkSections {
				if networkSections[i].ID == parts[1] {
					return i18n.T(lang, networkSections[i].OKKey), true
				}
			}
		}
	case "net-failed":
		if len(parts) < 3 {
			return "", false
		}
		sec := &networkSections[0]
		name := parts[1]
		for _, b := range sec.Backends {
			if b.ID == parts[1] {
				name = i18n.T(lang, b.NameKey)
			}
		}
		why := parts[2]
		switch why {
		case "invalid", "reserved", "taken", "consolebound", "stale", "noidentity", "unavailable":
		default:
			why = "unavailable"
		}
		msg := name + ": " + i18n.T(lang, sec.WhyKey(why))
		if len(parts) == 4 {
			var ok []string
			for _, id := range strings.Split(parts[3], ",") {
				for _, b := range sec.Backends {
					if b.ID == id {
						ok = append(ok, i18n.T(lang, b.NameKey))
					}
				}
			}
			msg = i18n.T(lang, "account.net.partial.done") + " " + strings.Join(ok, ", ") + ". " +
				i18n.T(lang, "account.net.partial.failed") + " " + msg + " " + i18n.T(lang, "account.net.partial.retry")
		}
		return msg, true
	}
	return "", false
}
