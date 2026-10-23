package pages

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// Region-node operators. A signed-in user applies from /account/operator for
// a location the coverage plan wants, describing the servers they plan to
// run; an admin reviews it in /admin/nodes/applications. Elsewhere they can
// join a per-country waitlist. The terms they accept are /operators.

// maxPlannedServers is how many server rows the form offers. A node needs
// fewer than this to meet the minimum with ordinary machines.
const maxPlannedServers = 6

// operatorRow is one planned-server row as the form shows it.
type operatorRow struct {
	N     int
	Plan  store.OperatorServerPlan
	Roles map[string]bool
}

type operatorView struct {
	baseData
	Available bool
	Req       *core.NodeRequirements
	Locations []core.NodeLocation // the open ones
	Roles     []string
	Apps      []store.OperatorApplication
	InReview  bool // an application is pending or waiting on the applicant
	Rows      []operatorRow
	Form      store.OperatorApplication
	Errors    []string
	NotWanted bool // the chosen place is not wanted: offer the waitlist
	Done      string
}

func (s *Server) handleOperator(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	s.renderOperator(w, r, v, operatorView{Done: r.URL.Query().Get("done"), Form: store.OperatorApplication{Email: v.Email}}, http.StatusOK)
}

func (s *Server) renderOperator(w http.ResponseWriter, r *http.Request, v *viewer, view operatorView, status int) {
	lang := i18n.FromRequest(r)
	view.baseData = s.data(r, v, i18n.T(lang, "operator.title"))
	view.Path = "/account/operator"
	if s.Federation != nil && s.Operators != nil {
		view.Available = true
		if rq, err := s.Federation.NodeRequirements(r.Context()); err == nil {
			view.Req = &rq
			view.Roles = rq.Roles
		} else {
			s.log.Error("node requirements", "err", err)
			view.Available = false
		}
		locs, err := s.Federation.ListLocations(r.Context())
		if err != nil {
			s.log.Error("node locations", "err", err)
		}
		for _, l := range locs {
			if l.Accepting {
				view.Locations = append(view.Locations, l)
			}
		}
		view.Apps, _ = s.Operators.OperatorApplicationsFor(r.Context(), v.AccountID)
		for _, a := range view.Apps {
			if a.Status == store.OpPending || a.Status == store.OpInfo {
				view.InReview = true
			}
		}
	}
	if len(view.Rows) == 0 {
		for i := range maxPlannedServers {
			view.Rows = append(view.Rows, operatorRow{N: i + 1, Roles: map[string]bool{}})
		}
	}
	s.render(w, status, "operator.html", view)
}

// parsePlan reads the server rows; a row with no cores is an unused row.
func parsePlan(r *http.Request, roles []string) ([]store.OperatorServerPlan, []operatorRow) {
	var plans []store.OperatorServerPlan
	var rows []operatorRow
	num := func(k string) int {
		n, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue(k)))
		if n < 0 || n > 1_000_000 {
			return 0
		}
		return n
	}
	for i := range maxPlannedServers {
		p := "s" + strconv.Itoa(i) + "_"
		plan := store.OperatorServerPlan{Cores: num(p + "cores"), RAMGB: num(p + "ram"), DiskGB: num(p + "disk"),
			DiskSSD: r.PostFormValue(p+"ssd") == "on", BandwidthMbps: num(p + "bw"), IPv6: r.PostFormValue(p+"ipv6") == "on"}
		picked := map[string]bool{}
		for _, role := range r.PostForm[p+"roles"] {
			for _, known := range roles {
				if role == known && !picked[role] {
					picked[role] = true
					plan.Roles = append(plan.Roles, role)
				}
			}
		}
		rows = append(rows, operatorRow{N: i + 1, Plan: plan, Roles: picked})
		if plan.Cores > 0 {
			plans = append(plans, plan)
		}
	}
	return plans, rows
}

// planProblems checks the described servers against the core's figures, so
// an applicant learns at once what falls short. It checks a description: the
// servers' own reports are what count once they run.
func planProblems(lang string, rq core.NodeRequirements, plans []store.OperatorServerPlan) []string {
	if len(plans) == 0 {
		return []string{i18n.T(lang, "operator.err.noservers")}
	}
	var out []string
	var cores, ram, disk int
	for i, p := range plans {
		cores += p.Cores
		ram += p.RAMGB
		if p.DiskSSD {
			disk += p.DiskGB
		}
		if p.Cores < rq.ServerCores || p.RAMGB < rq.ServerRAMGB || !p.DiskSSD || p.BandwidthMbps < rq.ServerBandwidthMbps || len(p.Roles) == 0 {
			out = append(out, fill(i18n.T(lang, "operator.err.server"), "n", i+1, "cores", rq.ServerCores, "ram", rq.ServerRAMGB, "bw", rq.ServerBandwidthMbps))
		}
	}
	if cores < rq.NodeCores {
		out = append(out, fill(i18n.T(lang, "operator.err.cores"), "have", cores, "want", rq.NodeCores))
	}
	if ram < rq.NodeRAMGB {
		out = append(out, fill(i18n.T(lang, "operator.err.ram"), "have", ram, "want", rq.NodeRAMGB))
	}
	if disk < rq.NodeDiskGB {
		out = append(out, fill(i18n.T(lang, "operator.err.disk"), "have", disk, "want", rq.NodeDiskGB))
	}
	return out
}

// fill replaces {name} placeholders in a catalogue string.
func fill(s string, kv ...any) string {
	for i := 0; i+1 < len(kv); i += 2 {
		s = strings.ReplaceAll(s, "{"+kv[i].(string)+"}", strconv.Itoa(kv[i+1].(int)))
	}
	return s
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && len(s) <= 254
}

func (s *Server) handleOperatorApply(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if s.Federation == nil || s.Operators == nil {
		http.Error(w, "applications are not available", http.StatusServiceUnavailable)
		return
	}
	lang := i18n.FromRequest(r)
	rq, err := s.Federation.NodeRequirements(r.Context())
	if err != nil {
		s.log.Error("node requirements", "err", err)
		http.Error(w, "applications are not available", http.StatusServiceUnavailable)
		return
	}
	plans, rows := parsePlan(r, rq.Roles)
	form := store.OperatorApplication{AccountID: v.AccountID, Location: strings.TrimSpace(r.PostFormValue("location")),
		City: trimTo(r.PostFormValue("city"), 64), Provider: trimTo(r.PostFormValue("provider"), 100),
		Email: strings.TrimSpace(r.PostFormValue("email")), Notes: trimTo(r.PostFormValue("notes"), 2000), Servers: plans}
	view := operatorView{Rows: rows, Form: form}
	if apps, _ := s.Operators.OperatorApplicationsFor(r.Context(), v.AccountID); apps != nil {
		for _, a := range apps {
			if a.Status == store.OpPending || a.Status == store.OpInfo {
				view.Errors = append(view.Errors, i18n.T(lang, "operator.err.pending"))
				s.renderOperator(w, r, v, view, http.StatusConflict)
				return
			}
		}
	}
	// Only an open location is taken. Anything else, a stale form or a
	// hand-made POST, gets the friendly refusal and the waitlist.
	var loc *core.NodeLocation
	if locs, err := s.Federation.ListLocations(r.Context()); err == nil {
		for i := range locs {
			if locs[i].Code == form.Location && locs[i].Accepting {
				loc = &locs[i]
			}
		}
	}
	if loc == nil {
		view.NotWanted = true
		view.Errors = append(view.Errors, i18n.T(lang, "operator.err.notwanted"))
		s.renderOperator(w, r, v, view, http.StatusUnprocessableEntity)
		return
	}
	form.Country = loc.Country
	if form.City == "" || form.Provider == "" || !validEmail(form.Email) {
		view.Errors = append(view.Errors, i18n.T(lang, "operator.err.fields"))
	}
	if r.PostFormValue("terms") != "on" {
		view.Errors = append(view.Errors, i18n.T(lang, "operator.err.terms"))
	}
	view.Errors = append(view.Errors, planProblems(lang, rq, plans)...)
	if len(view.Errors) > 0 {
		s.renderOperator(w, r, v, view, http.StatusUnprocessableEntity)
		return
	}
	form.TermsAt = time.Now()
	if _, err := s.Operators.CreateOperatorApplication(r.Context(), form); err != nil {
		s.log.Error("operator application", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/account/operator?done=applied", http.StatusSeeOther)
}

// handleOperatorWaitlist records interest in a country nobody is wanted in yet.
func (s *Server) handleOperatorWaitlist(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if s.Operators == nil {
		http.Error(w, "applications are not available", http.StatusServiceUnavailable)
		return
	}
	country := strings.ToUpper(strings.TrimSpace(r.PostFormValue("country")))
	email := strings.TrimSpace(r.PostFormValue("email"))
	if len(country) != 2 || !validEmail(email) {
		s.renderOperator(w, r, v, operatorView{NotWanted: true, Form: store.OperatorApplication{Email: email, Country: country},
			Errors: []string{i18n.T(i18n.FromRequest(r), "operator.err.waitlist")}}, http.StatusUnprocessableEntity)
		return
	}
	if apps, _ := s.Operators.OperatorApplicationsFor(r.Context(), v.AccountID); apps != nil {
		for _, a := range apps {
			if a.Status == store.OpWaitlist && a.Country == country {
				http.Redirect(w, r, "/account/operator?done=waitlist", http.StatusSeeOther)
				return
			}
		}
	}
	_, err := s.Operators.CreateOperatorApplication(r.Context(), store.OperatorApplication{AccountID: v.AccountID, Email: email,
		Country: country, City: trimTo(r.PostFormValue("city"), 64), Status: store.OpWaitlist, TermsAt: time.Now()})
	if err != nil {
		s.log.Error("operator waitlist", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/account/operator?done=waitlist", http.StatusSeeOther)
}

// handleOperatorReply answers an admin's question; the application goes back
// to review.
func (s *Server) handleOperatorReply(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if s.Operators == nil {
		http.Error(w, "applications are not available", http.StatusServiceUnavailable)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	reply := trimTo(r.PostFormValue("reply"), 2000)
	if reply == "" {
		http.Redirect(w, r, "/account/operator", http.StatusSeeOther)
		return
	}
	if err := s.Operators.ReplyOperatorApplication(r.Context(), id, v.AccountID, reply); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/account/operator?done=replied", http.StatusSeeOther)
}

func trimTo(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

// operatorsTermsView is /operators: the terms, with the core's figures.
type operatorsTermsView struct {
	baseData
	Req           *core.NodeRequirements
	Stale, MaxAge string
}

// humanDur prints 3m as "3 minutes" and 72h as "72 hours".
func humanDur(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return strconv.Itoa(int(d/time.Hour)) + " hours"
	}
	return strconv.Itoa(int(d/time.Minute)) + " minutes"
}

func (s *Server) handleOperatorsTerms(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	view := operatorsTermsView{baseData: s.data(r, v, i18n.T(i18n.FromRequest(r), "footer.operators"))}
	if s.Federation != nil {
		if rq, err := s.Federation.NodeRequirements(r.Context()); err == nil {
			view.Req = &rq
			view.Stale, view.MaxAge = humanDur(rq.ReportStale), humanDur(rq.SelfTestMaxAge)
		}
	}
	s.render(w, http.StatusOK, "operators.html", view)
}
