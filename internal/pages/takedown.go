package pages

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// The "Report a takedown" form on the trademarks and takedowns page. It opens a help ticket tagged
// "takedown" (store.TicketCategoryTakedown) so a notice never depends on the takedown@ inbox alone.
// Anyone can send one, signed in or not: a rights holder has no reason to have an OpenPak account.
// Anonymous notices are held against the placeholder account below and are answered by email, to the
// address in the notice; a signed-in sender can also follow the ticket under Help.

// anonymousClaimant is the account id of a notice sent without signing in.
const anonymousClaimant = "takedown:anonymous"

type takedownForm struct {
	Off                                         bool // help tickets are not available: only the email address is offered
	Turnstile                                   string
	Name, Email, OnBehalf, Work, Material, Sign string
	Error                                       string
}

type legalView struct {
	baseData
	Takedown takedownForm
}

func (s *Server) handleLegal(w http.ResponseWriter, r *http.Request) {
	v := s.viewerOf(w, r)
	lang := i18n.FromRequest(r)
	d := legalView{baseData: s.data(r, v, i18n.T(lang, "footer.legal"))}
	d.Takedown = s.takedownDefaults(v)
	s.render(w, http.StatusOK, legalTemplate("legal", lang), d)
}

func (s *Server) takedownDefaults(v *viewer) takedownForm {
	f := takedownForm{Off: !s.helpOn(), Turnstile: s.Turnstile.Key()}
	if v != nil {
		f.Name, f.Email = v.DisplayName, v.Email
	}
	return f
}

func limitText(v string, n int) string {
	v = strings.ReplaceAll(strings.TrimSpace(v), "\r\n", "\n")
	if utf8.RuneCountInString(v) > n {
		return string([]rune(v)[:n])
	}
	return v
}

func (s *Server) handleTakedown(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if !s.helpOn() {
		http.Error(w, "help tickets are not available", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	v := s.viewerOf(w, r)
	// A signed-in sender's form carries the session's CSRF token like every other post; an anonymous
	// one has no session to protect, and is held back by the rate limit and the human check instead.
	if v != nil && !s.Sessions.CheckCSRF(v.CoreSessionID, r.PostFormValue("csrf")) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	if !s.humanCheck(w, r, "footer.legal") {
		return
	}
	lang := i18n.FromRequest(r)
	f := takedownForm{Off: false, Turnstile: s.Turnstile.Key(),
		Name: limitText(r.PostFormValue("name"), 120), Email: limitText(r.PostFormValue("email"), 200),
		OnBehalf: limitText(r.PostFormValue("behalf"), 200), Work: limitText(r.PostFormValue("work"), 3000),
		Material: limitText(r.PostFormValue("material"), 3000), Sign: limitText(r.PostFormValue("signature"), 120)}
	fail := func(msg string) {
		f.Error = msg
		s.render(w, http.StatusBadRequest, legalTemplate("legal", lang), legalView{s.data(r, v, i18n.T(lang, "footer.legal")), f})
	}
	switch {
	case f.Name == "" || f.Work == "" || f.Material == "" || f.Sign == "":
		fail(i18n.T(lang, "takedown.invalid"))
		return
	case !strings.Contains(f.Email, "@") || strings.ContainsAny(f.Email, " \t\r\n<>"):
		fail(i18n.T(lang, "takedown.email.invalid"))
		return
	case r.PostFormValue("goodfaith") != "1" || r.PostFormValue("accurate") != "1":
		fail(i18n.T(lang, "takedown.statements.required"))
		return
	}
	account, signedIn := anonymousClaimant, "not signed in"
	if v != nil {
		account, signedIn = v.AccountID, "signed in as "+v.AccountID
	}
	subject := "Takedown notice: " + strings.Join(strings.Fields(f.Material), " ")
	if utf8.RuneCountInString(subject) > 120 {
		subject = string([]rune(subject)[:120]) + "…"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Takedown notice from the form on the legal page.\n\nName: %s\nEmail: %s\n", f.Name, f.Email)
	if f.OnBehalf != "" {
		fmt.Fprintf(&b, "On behalf of: %s\n", f.OnBehalf)
	}
	fmt.Fprintf(&b, "Signed: %s\nSender: %s\n\nWork believed to be infringed:\n%s\n\nMaterial (address or description):\n%s\n\n", f.Sign, signedIn, f.Work, f.Material)
	b.WriteString("The sender confirmed: a good-faith belief that the use is not authorised by the owner, its agent or the law; and that the information is accurate and they are the owner or authorised to act for the owner.\n")
	t, err := s.Support.Open(r.Context(), store.Ticket{AccountID: account, DisplayName: f.Name, Email: f.Email,
		Subject: subject, Category: store.TicketCategoryTakedown}, b.String(), nil)
	if err != nil {
		s.log.Error("takedown: open ticket", "err", err)
		fail(i18n.T(lang, "msg.error"))
		return
	}
	s.log.Info("takedown notice received", "ticket", t.ID, "signed_in", v != nil)
	s.render(w, http.StatusOK, "takedown_sent.html", struct {
		baseData
		ID     int64
		Follow bool
	}{s.data(r, v, i18n.T(lang, "footer.legal")), t.ID, v != nil})
}
