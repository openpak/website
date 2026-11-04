package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
	"github.com/openpak/website/internal/support"
)

func validNotice() url.Values {
	return url.Values{"name": {"Pat Rights"}, "email": {"pat@rights.example"}, "behalf": {"Example Games Ltd"},
		"work": {"Example Game, 2019"}, "material": {"https://openpak.org/x/example"}, "signature": {"Pat Rights"},
		"goodfaith": {"1"}, "accurate": {"1"}}
}

func postAnon(s *Server, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/legal/takedown", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// The legal pages name takedown@openpak.org as the contact for notices, in every language.
func TestLegalPagesUseTheTakedownAddress(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, lang := range i18n.Supported() {
		for _, path := range []string{"/terms", "/privacy", "/legal"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept-Language", lang)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "mailto:takedown@openpak.org") {
				t.Errorf("%s (%s) does not give takedown@openpak.org (%d)", path, lang, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "{{") {
				t.Errorf("%s (%s) shows template syntax", path, lang)
			}
		}
	}
}

// Without help tickets the page offers only the address, never a form that cannot work.
func TestTakedownFormHiddenWithoutTickets(t *testing.T) {
	s, _, _ := newTestServer(t)
	body := get(t, s, "/legal").Body.String()
	if strings.Contains(body, `action="/legal/takedown"`) {
		t.Error("form shown with help tickets off")
	}
	if rec := postAnon(s, validNotice()); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("post with tickets off: %d", rec.Code)
	}
}

func TestTakedownFormOpensATaggedTicket(t *testing.T) {
	s, fc, db := newTestServer(t)
	s.Support = &support.Service{DB: db}
	body := get(t, s, "/legal").Body.String()
	for _, want := range []string{`action="/legal/takedown"`, "Report a takedown", "Your signature", `id="report"`} {
		if !strings.Contains(body, want) {
			t.Errorf("legal page lacks %q", want)
		}
	}

	// Signed out: a ticket tagged takedown, held against the placeholder account, naming the sender.
	rec := postAnon(s, validNotice())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ticket #1") {
		t.Fatalf("anonymous notice: %d %s", rec.Code, rec.Body.String())
	}
	tk, err := db.GetTicket(context.Background(), 1)
	if err != nil || tk.Category != store.TicketCategoryTakedown || tk.AccountID != anonymousClaimant || tk.Email != "pat@rights.example" || tk.DisplayName != "Pat Rights" {
		t.Fatalf("ticket: %+v %v", tk, err)
	}
	msgs, _ := db.TicketMessages(context.Background(), 1, true)
	for _, want := range []string{"Pat Rights", "pat@rights.example", "Example Games Ltd", "Example Game, 2019", "https://openpak.org/x/example", "not signed in", "good-faith"} {
		if len(msgs) != 1 || !strings.Contains(msgs[0].Body, want) {
			t.Errorf("ticket text lacks %q", want)
		}
	}
	// Nobody signed in can read the anonymous ticket as their own.
	a, _ := seedTwoAccounts(fc)
	la := login(t, s, a, "password-A-1")
	if rec := authedRequest(s, la, http.MethodGet, "/help/1", nil); rec.Code != http.StatusNotFound {
		t.Errorf("anonymous notice readable by an account: %d", rec.Code)
	}

	// Signed in: the notice belongs to the account, which can follow it under Help.
	form := validNotice()
	rec = authedRequest(s, la, http.MethodPost, "/legal/takedown", &form)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `href="/help/2"`) {
		t.Fatalf("signed-in notice: %d %s", rec.Code, rec.Body.String())
	}
	if tk, _ := db.GetTicket(context.Background(), 2); tk.AccountID == anonymousClaimant || tk.AccountID == "" || tk.Category != "takedown" {
		t.Errorf("signed-in ticket: %+v", tk)
	}
	if rec := authedRequest(s, la, http.MethodGet, "/help/2", nil); rec.Code != http.StatusOK {
		t.Errorf("own takedown thread: %d", rec.Code)
	}
	if rec := authedRequest(s, la, http.MethodGet, "/help", nil); !strings.Contains(rec.Body.String(), "Takedown notice") || strings.Contains(rec.Body.String(), "help.cat.takedown") {
		t.Error("the help list does not label a takedown ticket")
	}
}

func TestTakedownFormValidationAndCSRF(t *testing.T) {
	s, fc, db := newTestServer(t)
	s.Support = &support.Service{DB: db}
	for name, mut := range map[string]func(url.Values){
		"no name":        func(f url.Values) { f.Del("name") },
		"no work":        func(f url.Values) { f.Set("work", " ") },
		"no signature":   func(f url.Values) { f.Del("signature") },
		"bad email":      func(f url.Values) { f.Set("email", "not an address") },
		"no good faith":  func(f url.Values) { f.Del("goodfaith") },
		"no accuracy":    func(f url.Values) { f.Del("accurate") },
		"header in mail": func(f url.Values) { f.Set("email", "a@b.example>\nBcc: x@y.example") },
	} {
		f := validNotice()
		mut(f)
		rec := postAnon(s, f)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `id="report"`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if n, _ := db.ListTickets(context.Background(), store.TicketFilter{}, 10); len(n) != 0 {
		t.Errorf("an invalid notice opened a ticket: %d", len(n))
	}
	// A signed-in post without the CSRF token is refused.
	a, _ := seedTwoAccounts(fc)
	la := login(t, s, a, "password-A-1")
	req := httptest.NewRequest(http.MethodPost, "/legal/takedown", strings.NewReader(validNotice().Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(la.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("forged signed-in notice: %d", rec.Code)
	}
}
