package pages

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pakkachat"
)

// Pakka, the assistant, on the site: the chat panel at /account/pakka, the
// JSON and event-stream calls it makes under /account/pakka/api, and the
// account-scoped reads Pakka itself asks for (pakkachat.Tools).

// PakkaMinAge is the age Meta's terms set for people using the model behind Pakka.
const PakkaMinAge = 18

// PakkaEligible says whether accountID may use Pakka: an active account whose
// recorded birthday makes it an adult. A missing or unreadable birthday, a
// failed lookup, and anything else uncertain all answer no. There is no
// parental override.
func (s *Server) PakkaEligible(ctx context.Context, accountID string) bool {
	if s.Identity == nil || accountID == "" {
		return false
	}
	acct, err := s.Identity.GetAccount(ctx, accountID)
	if err != nil || acct.Status != "active" {
		return false
	}
	return AdultBirthday(acct.Birthday, time.Now())
}

// AdultBirthday reports whether a YYYY-MM-DD birthday is 18 or more years before now.
func AdultBirthday(birthday string, now time.Time) bool {
	b, err := time.Parse("2006-01-02", strings.TrimSpace(birthday))
	if err != nil || b.After(now) {
		return false
	}
	return !b.AddDate(PakkaMinAge, 0, 0).After(now)
}

type pakkaTools struct{ s *Server }

// PakkaTools is the data Pakka may read for a signed-in person.
func (s *Server) PakkaTools() pakkachat.Tools { return pakkaTools{s} }

func (t pakkaTools) Eligible(ctx context.Context, p pakkachat.Principal) bool {
	return t.s.PakkaEligible(ctx, p.AccountID)
}

// Consoles names the platforms linked to the account; no identifiers.
func (t pakkaTools) Consoles(ctx context.Context, p pakkachat.Principal) (any, error) {
	names := []string{}
	for _, l := range []struct{ ns, name string }{{"switch", "Switch"}, {"wiiu", "Wii U"}, {"3ds", "3DS"}, {"wfc", "Wii / DS"}} {
		if _, err := t.s.Links.ActiveLinkByAccount(ctx, l.ns, p.AccountID); err == nil {
			names = append(names, l.name)
		}
	}
	return map[string]any{"linked_consoles": names, "count": len(names)}, nil
}

// Friends is a count only: no names, no codes.
func (t pakkaTools) Friends(ctx context.Context, p pakkachat.Principal) (any, error) {
	fs, err := t.s.Social.ListFriends(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"friends": len(fs)}, nil
}

// Rewards is the balance and mission progress of the session's own account.
func (t pakkaTools) Rewards(ctx context.Context, p pakkachat.Principal) (any, error) {
	if t.s.Rewards == nil {
		return map[string]any{"available": false}, nil
	}
	me, err := t.s.Rewards.Me(ctx, p.CoreSessionID)
	if err != nil {
		return nil, err
	}
	type row struct {
		Title    string `json:"title"`
		Progress int64  `json:"progress"`
		Target   int64  `json:"target"`
		Claimed  bool   `json:"claimed"`
		Claim    bool   `json:"ready_to_claim"`
	}
	rows := []row{}
	if ms, err := t.s.Rewards.Missions(ctx, p.CoreSessionID); err == nil {
		for _, m := range ms {
			if len(rows) == 12 {
				break
			}
			rows = append(rows, row{m.Title, m.Progress, m.Target, m.Claimed, m.Claimable && !m.Claimed && !m.Locked})
		}
	}
	return map[string]any{"balance": me.Account.Balance, "lifetime_earned": me.Account.LifetimeEarned,
		"frozen": me.Account.Frozen, "paused": me.Account.Paused, "missions": rows}, nil
}

// Status is what the public status page says about OpenPak's own services.
func (t pakkaTools) Status(ctx context.Context) (any, error) {
	up := func(err error) string {
		if err == nil {
			return "up"
		}
		return "down"
	}
	out := map[string]any{"accounts": up(t.s.Core.Healthy(ctx)), "database": up(t.s.DB.Ping(ctx))}
	if t.s.Media != nil {
		out["storage"] = up(t.s.Media.Healthy(ctx))
	}
	down := []string{}
	checked := 0
	for _, g := range CheckGameServers(ctx, TitleCatalogue(ctx, t.s.DB)) {
		if g.Checkable {
			checked++
			if !g.Up {
				down = append(down, g.Name)
			}
		}
	}
	out["game_servers_checked"] = checked
	out["game_servers_down"] = down
	out["status_page"] = "https://openpak.org/status"
	return out, nil
}

// pakkaStrings are the panel's words, handed to the script as JSON so the page
// needs no inline script and the script holds no copy of its own.
var pakkaStringKeys = []string{
	"pakka.title", "pakka.sub", "pakka.ai", "pakka.privacy", "pakka.new", "pakka.history", "pakka.empty", "pakka.delete",
	"pakka.delete.confirm", "pakka.placeholder", "pakka.send", "pakka.typing", "pakka.looking", "pakka.error", "pakka.retry",
	"pakka.limit", "pakka.daily", "pakka.busy", "pakka.ticket", "pakka.unavailable", "pakka.you", "pakka.welcome",
	"pakka.too_long", "pakka.conversation_limit", "pakka.session", "pakka.untitled", "pakka.deleted",
	"pakka.notice.view", "pakka.notice.close", "pakka.notice.withdraw", "pakka.notice.accepted", "pakka.consent_lost",
}

type pakkaView struct {
	baseData
	Available bool
	Eligible  bool
	JSON      string
}

func (s *Server) handlePakka(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	d := s.data(r, v, i18n.T(i18n.FromRequest(r), "pakka.title"))
	page := pakkaView{baseData: d, Available: s.Pakka != nil}
	if s.Pakka != nil {
		page.Eligible = s.PakkaEligible(r.Context(), v.AccountID)
	}
	if page.Eligible {
		m := map[string]string{}
		for _, k := range pakkaStringKeys {
			m[k] = i18n.T(d.Lang, k)
		}
		b, _ := json.Marshal(m)
		page.JSON = string(b)
	}
	s.render(w, http.StatusOK, "pakka.html", page)
}

// pakkaAPI serves one chat call from the page. The person is the cookie
// session; changes need the page's CSRF token in a header.
func (s *Server) pakkaAPI(rt pakkachat.Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Pakka == nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		v := s.viewerOf(w, r)
		if v == nil {
			http.Error(w, `{"error":"sign in","code":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if rt.Mutates && !s.Sessions.CheckCSRF(v.CoreSessionID, r.Header.Get("X-CSRF")) {
			http.Error(w, `{"error":"invalid CSRF token","code":"forbidden"}`, http.StatusForbidden)
			return
		}
		s.Pakka.Forward(w, r, pakkachat.Principal{AccountID: v.AccountID, CoreSessionID: v.CoreSessionID}, rt)
	}
}

func (s *Server) registerPakka(mux *http.ServeMux) {
	mux.HandleFunc("GET /account/pakka", s.handlePakka)
	for _, rt := range pakkachat.Routes {
		mux.HandleFunc(rt.Method+" /account/pakka/api"+rt.Pattern, s.pakkaAPI(rt))
	}
}

// pakkaOK is whether to draw Pakka entry points for v: Pakka is configured
// and the account may use it. Anyone else sees no trace of it.
func (s *Server) pakkaOK(ctx context.Context, v *viewer) bool {
	return s.Pakka != nil && v != nil && s.PakkaEligible(ctx, v.AccountID)
}
