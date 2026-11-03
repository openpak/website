package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pages"
	"github.com/openpak/website/internal/store"
)

// Running a region node, for the app: the same flow as /account/operator.
//
//	GET  /api/v1/me/operator            requirements, open locations, roles and the account's applications
//	POST /api/v1/me/operator            apply (location, city, provider, email, notes, terms, servers[])
//	POST /api/v1/me/operator/waitlist   {"country","city","email"} for a place nobody is wanted in yet
//	POST /api/v1/me/operator/reply      {"id","reply"} answers an admin's question
//
// Validation failures are 422 with {"errors":[...]} in the caller's language.

func (s *Server) registerOperator(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/operator", s.auth(s.handleOperator))
	mux.HandleFunc("POST /api/v1/me/operator", s.auth(s.handleOperatorApply))
	mux.HandleFunc("POST /api/v1/me/operator/waitlist", s.auth(s.handleOperatorWaitlist))
	mux.HandleFunc("POST /api/v1/me/operator/reply", s.auth(s.handleOperatorReply))
}

func (s *Server) operatorOff(w http.ResponseWriter) bool {
	if s.Federation == nil || s.Operators == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "applications are not available", "code": "operator_unavailable", "available": false})
		return true
	}
	return false
}

func operatorErrors(w http.ResponseWriter, status int, errs ...string) {
	writeJSON(w, status, map[string]any{"error": "the application was not accepted", "code": "invalid_application", "errors": errs})
}

func (s *Server) handleOperator(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.operatorOff(w) {
		return
	}
	rq, err := s.Federation.NodeRequirements(r.Context())
	if err != nil {
		s.log.Error("api: node requirements", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "applications are not available", "code": "operator_unavailable", "available": false})
		return
	}
	locs := []map[string]string{}
	if all, err := s.Federation.ListLocations(r.Context()); err == nil {
		for _, l := range all {
			if l.Accepting {
				locs = append(locs, map[string]string{"code": l.Code, "country": l.Country, "city": l.City})
			}
		}
	}
	apps, _ := s.Operators.OperatorApplicationsFor(r.Context(), tok.AccountID)
	out := []map[string]any{}
	inReview := false
	for _, a := range apps {
		if a.Status == store.OpPending || a.Status == store.OpInfo {
			inReview = true
		}
		servers := a.Servers
		if servers == nil {
			servers = []store.OperatorServerPlan{}
		}
		out = append(out, map[string]any{"id": a.ID, "status": a.Status, "location": a.Location, "country": a.Country, "city": a.City,
			"provider": a.Provider, "email": a.Email, "notes": a.Notes, "reply": a.Reply, "admin_note": a.AdminNote,
			"servers": servers, "created_at": a.CreatedAt.UTC().Format(time.RFC3339), "updated_at": a.UpdatedAt.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true, "in_review": inReview, "roles": rq.Roles, "locations": locs, "applications": out,
		"max_servers": pages.MaxPlannedServers,
		"requirements": map[string]any{
			"node_cores": rq.NodeCores, "node_ram_gb": rq.NodeRAMGB, "node_disk_gb": rq.NodeDiskGB,
			"server_cores": rq.ServerCores, "server_ram_gb": rq.ServerRAMGB, "server_bandwidth_mbps": rq.ServerBandwidthMbps,
			"availability_pct": rq.AvailabilityPct,
		},
		"terms_url": "https://openpak.org/operators",
	})
}

func (s *Server) handleOperatorApply(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.operatorOff(w) {
		return
	}
	lang := apiLang(r)
	var in struct {
		Location string                     `json:"location"`
		City     string                     `json:"city"`
		Provider string                     `json:"provider"`
		Email    string                     `json:"email"`
		Notes    string                     `json:"notes"`
		Terms    bool                       `json:"terms"`
		Servers  []store.OperatorServerPlan `json:"servers"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if !decodeJSON(w, r, &in) {
		return
	}
	rq, err := s.Federation.NodeRequirements(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "applications are not available")
		return
	}
	if apps, _ := s.Operators.OperatorApplicationsFor(r.Context(), tok.AccountID); apps != nil {
		for _, a := range apps {
			if a.Status == store.OpPending || a.Status == store.OpInfo {
				operatorErrors(w, http.StatusConflict, i18n.T(lang, "operator.err.pending"))
				return
			}
		}
	}
	var loc *struct{ country string }
	if locs, err := s.Federation.ListLocations(r.Context()); err == nil {
		for _, l := range locs {
			if l.Code == strings.TrimSpace(in.Location) && l.Accepting {
				loc = &struct{ country string }{l.Country}
			}
		}
	}
	if loc == nil {
		operatorErrors(w, http.StatusUnprocessableEntity, i18n.T(lang, "operator.err.notwanted"))
		return
	}
	if pages.ExcludedCountry(loc.country) {
		operatorErrors(w, http.StatusUnprocessableEntity, i18n.T(lang, "operator.err.excluded"))
		return
	}
	if len(in.Servers) > pages.MaxPlannedServers {
		in.Servers = in.Servers[:pages.MaxPlannedServers]
	}
	known := map[string]bool{}
	for _, role := range rq.Roles {
		known[role] = true
	}
	var plans []store.OperatorServerPlan
	for _, p := range in.Servers {
		if p.Cores <= 0 {
			continue
		}
		var roles []string
		seen := map[string]bool{}
		for _, role := range p.Roles {
			if known[role] && !seen[role] {
				seen[role] = true
				roles = append(roles, role)
			}
		}
		p.Roles = roles
		p.Cores, p.RAMGB, p.DiskGB, p.BandwidthMbps = clampInt(p.Cores), clampInt(p.RAMGB), clampInt(p.DiskGB), clampInt(p.BandwidthMbps)
		plans = append(plans, p)
	}
	form := store.OperatorApplication{AccountID: tok.AccountID, Location: strings.TrimSpace(in.Location), Country: loc.country,
		City: pages.TrimTo(in.City, 64), Provider: pages.TrimTo(in.Provider, 100), Email: strings.TrimSpace(in.Email),
		Notes: pages.TrimTo(in.Notes, 2000), Servers: plans}
	var errs []string
	if form.City == "" || form.Provider == "" || !pages.ValidEmail(form.Email) {
		errs = append(errs, i18n.T(lang, "operator.err.fields"))
	}
	if !in.Terms {
		errs = append(errs, i18n.T(lang, "operator.err.terms"))
	}
	errs = append(errs, pages.PlanProblems(lang, rq, plans)...)
	if len(errs) > 0 {
		operatorErrors(w, http.StatusUnprocessableEntity, errs...)
		return
	}
	form.TermsAt = time.Now()
	if _, err := s.Operators.CreateOperatorApplication(r.Context(), form); err != nil {
		s.log.Error("api: operator application", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func clampInt(n int) int {
	if n < 0 || n > 1_000_000 {
		return 0
	}
	return n
}

func (s *Server) handleOperatorWaitlist(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.Operators == nil {
		s.operatorOff(w)
		return
	}
	lang := apiLang(r)
	var in struct{ Country, City, Email string }
	if !decodeSmall(w, r, &in) {
		return
	}
	country, email := strings.ToUpper(strings.TrimSpace(in.Country)), strings.TrimSpace(in.Email)
	if len(country) != 2 || !pages.ValidEmail(email) {
		operatorErrors(w, http.StatusUnprocessableEntity, i18n.T(lang, "operator.err.waitlist"))
		return
	}
	if pages.ExcludedCountry(country) {
		operatorErrors(w, http.StatusUnprocessableEntity, i18n.T(lang, "operator.err.excluded"))
		return
	}
	if apps, _ := s.Operators.OperatorApplicationsFor(r.Context(), tok.AccountID); apps != nil {
		for _, a := range apps {
			if a.Status == store.OpWaitlist && a.Country == country {
				writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
				return
			}
		}
	}
	if _, err := s.Operators.CreateOperatorApplication(r.Context(), store.OperatorApplication{AccountID: tok.AccountID, Email: email,
		Country: country, City: pages.TrimTo(in.City, 64), Status: store.OpWaitlist, TermsAt: time.Now()}); err != nil {
		s.log.Error("api: operator waitlist", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) handleOperatorReply(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if s.Operators == nil {
		s.operatorOff(w)
		return
	}
	var in struct {
		ID    int64
		Reply string
	}
	if !decodeSmall(w, r, &in) {
		return
	}
	reply := pages.TrimTo(in.Reply, 2000)
	if reply == "" {
		writeError(w, http.StatusBadRequest, "reply is required")
		return
	}
	if err := s.Operators.ReplyOperatorApplication(r.Context(), in.ID, tok.AccountID, reply); err != nil {
		writeError(w, http.StatusNotFound, "no such application")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// decodeJSON reads a body already capped by the caller.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
