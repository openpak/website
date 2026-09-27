package pages

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pctl"
)

// Parental controls, the web half of the app's screen: the same fields, in the
// same order, with the same words. The PIN is behind the account password
// (and the second factor, where the account has one) on every reveal, and is
// never in a URL: the reveal is a POST whose answer is the page itself.

type parentalView struct {
	baseData
	Available bool
	Code      *pctl.Device // the live pairing code, if one is waiting
	Consoles  []consoleView
	TwoFactor bool
	PinFor    string // the console whose PIN was just revealed
	Pin       string
	PinError  string
	PinOpen   string // the console whose password form is open
}

type consoleView struct {
	pctl.Device
	Name         string
	LastSynced   string
	Restrictions []fact
	WrongPins    []string
}

type fact struct{ Label, Value, Note string }

func (s *Server) handleParental(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	s.renderParental(w, r, v, parentalView{PinOpen: r.URL.Query().Get("pin")})
}

func (s *Server) renderParental(w http.ResponseWriter, r *http.Request, v *viewer, view parentalView) {
	lang := i18n.FromRequest(r)
	view.baseData = s.data(r, v, i18n.T(lang, "parental.title"))
	devices, err := s.Parental.Devices(r.Context(), v.AccountID)
	if err != nil {
		if !errors.Is(err, pctl.ErrUnavailable) {
			s.log.Error("parental devices", "err", err)
		}
		s.render(w, http.StatusOK, "parental.html", view)
		return
	}
	view.Available = true
	for i, d := range devices {
		if !d.Paired && d.PairingCode != "" && view.Code == nil {
			view.Code = &devices[i]
		}
		if d.Paired {
			view.Consoles = append(view.Consoles, consoleOf(lang, d))
		}
	}
	if me, err := s.Core.Whoami(r.Context(), v.CoreSessionID); err == nil {
		view.TwoFactor = me.TwoFactorEnabled
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, http.StatusOK, "parental.html", view)
}

// handleParentalPair mints a code, as the app's "Pair a console" does.
func (s *Server) handleParentalPair(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	if _, err := s.Parental.Pair(r.Context(), v.AccountID, v.DisplayName); err != nil {
		s.log.Error("parental pair", "err", err)
	}
	http.Redirect(w, r, "/account/parental", http.StatusSeeOther)
}

// handleParentalPin reveals one console's PIN once the password, and the second
// factor where there is one, has been given again: a signed-in browser someone
// else is sitting at must not be enough.
func (s *Server) handleParentalPin(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	lang := i18n.FromRequest(r)
	moon := r.PostFormValue("moon_id")
	view := parentalView{PinOpen: moon}
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return
	}
	err := accounts.Reauthenticate(r.Context(), s.Identity, v.AccountID, r.PostFormValue("password"), r.PostFormValue("code"))
	switch {
	case errors.Is(err, core.ErrInvalidCredentials), errors.Is(err, core.ErrSecondFactorRequired), errors.Is(err, core.ErrInvalidCode):
		view.PinError = i18n.T(lang, "parental.pin.wrong")
	case errors.Is(err, core.ErrTwoFactorLocked):
		view.PinError = i18n.T(lang, "auth.2fa.locked")
	case err != nil:
		s.log.Error("parental pin step-up", "err", err)
		http.Error(w, "the account service did not answer", http.StatusBadGateway)
		return
	default:
		code, err := s.Parental.UnlockCode(r.Context(), v.AccountID, moon)
		if err != nil {
			s.log.Error("parental pin", "err", err)
		}
		view.PinOpen, view.PinFor, view.Pin = "", moon, code
		if code == "" {
			view.PinError = i18n.T(lang, "parental.pin.unknown")
		}
	}
	s.renderParental(w, r, v, view)
}

// handleParentalPlayerImage relays a player's picture for the page's <img>.
// nx-baas refuses a console this account does not manage with a 404.
func (s *Server) handleParentalPlayerImage(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	img, err := s.Parental.PlayerImage(r.Context(), v.AccountID, r.PathValue("moonID"), r.PathValue("playerID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(img)
}

// consoleOf is one paired console as the page shows it.
func consoleOf(lang string, d pctl.Device) consoleView {
	c := consoleView{Device: d, Name: d.Nickname}
	if c.Name == "" {
		c.Name = i18n.T(lang, "parental.console")
	}
	c.LastSynced = i18n.T(lang, "parental.notsynced")
	if d.SyncedAt > 0 {
		c.LastSynced = fmt.Sprintf(i18n.T(lang, "parental.lastsynced"), when(lang, d.SyncedAt))
	}
	for _, at := range d.WrongPinAttempts {
		c.WrongPins = append(c.WrongPins, when(lang, at))
	}
	c.Restrictions = restrictions(lang, d.Setting)
	return c
}

func when(lang string, unix int64) string {
	t := time.Unix(unix, 0).UTC()
	return i18n.Date(lang, t) + " " + t.Format("15:04") + " UTC"
}

// restrictions reads the setting document the console is served into the rows
// the app shows, in the app's order. Values the page has no words for are
// shown as they are.
func restrictions(lang string, doc map[string]any) []fact {
	word := func(prefix, v string) string {
		if s := i18n.T(lang, prefix+v); s != prefix+v {
			return s
		}
		return v
	}
	restriction := func(v any) string {
		switch v {
		case "RESTRICTED", "UNRESTRICTED":
			return i18n.T(lang, "parental.r."+v.(string))
		}
		return i18n.T(lang, "parental.r.unset")
	}
	level, _ := doc["functionalRestrictionLevel"].(string)
	out := []fact{{Label: i18n.T(lang, "parental.level"), Value: word("parental.level.", level)}}
	if level == "CUSTOM" {
		custom, _ := doc["customSettings"].(map[string]any)
		age, _ := custom["ageRestriction"].(map[string]any)
		limit := i18n.T(lang, "parental.age.none")
		if n, ok := age["ratingAge"].(float64); ok && age["restriction"] == "RESTRICTED" {
			limit = fmt.Sprintf(i18n.T(lang, "parental.age.upto"), int(n))
		}
		out = append(out, fact{Label: i18n.T(lang, "parental.age"), Value: limit})
		if org, ok := custom["ratingOrganization"].(string); ok {
			if name, ok := ratingOrgs[org]; ok {
				org = name
			}
			out = append(out, fact{Label: i18n.T(lang, "parental.rating"), Value: org})
		}
		ugc, _ := custom["ugcRestriction"].(map[string]any)
		f := fact{Label: i18n.T(lang, "parental.ugc"), Value: restriction(ugc["restriction"])}
		if apps, _ := ugc["applications"].(map[string]any); len(apps) > 0 {
			f.Note = count(lang, "parental.ugc.apps", len(apps))
		}
		out = append(out, f,
			fact{Label: i18n.T(lang, "parental.sns"), Value: restriction(custom["snsPostRestriction"])},
			fact{Label: i18n.T(lang, "parental.vr"), Value: restriction(custom["vrRestriction"])})
	}
	timer, ok := doc["playTimerRegulations"].(map[string]any)
	if !ok {
		return out
	}
	day := func(label string, reg map[string]any) {
		play, _ := reg["timeToPlayInOneDay"].(map[string]any)
		value := i18n.T(lang, "parental.playtime.none")
		if n, ok := play["limitTime"].(float64); ok && play["enabled"] == true {
			value = count(lang, "parental.playtime.limit", int(n))
		}
		out = append(out, fact{Label: label, Value: value})
		if bed, _ := reg["bedtime"].(map[string]any); bed["enabled"] == true {
			value := fmt.Sprintf(i18n.T(lang, "parental.bedtime.from"), clock(bed["startingTime"]))
			if end, ok := bed["endingTime"]; ok {
				value = fmt.Sprintf(i18n.T(lang, "parental.bedtime.range"), clock(bed["startingTime"]), clock(end))
			}
			out = append(out, fact{Label: i18n.T(lang, "parental.bedtime"), Value: value})
		}
	}
	if timer["timerMode"] == "EACH_DAY_OF_THE_WEEK" {
		days, _ := timer["eachDayOfTheWeekRegulations"].(map[string]any)
		for _, name := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
			if reg, ok := days[name].(map[string]any); ok {
				day(i18n.T(lang, "parental.day."+name), reg)
			}
		}
	} else if reg, ok := timer["dailyRegulations"].(map[string]any); ok {
		day(i18n.T(lang, "parental.playtime"), reg)
	}
	if mode, ok := timer["restrictionMode"].(string); ok {
		out = append(out, fact{Label: i18n.T(lang, "parental.timeup"), Value: word("parental.timeup.", mode)})
	}
	return out
}

// ratingOrgs are the rating systems by their own names, the same in every
// language.
var ratingOrgs = map[string]string{
	"GRAC_GCRB": "GRAC/GCRB", "CLASS_IND": "ClassInd", "PEGI_PORTUGAL": "PEGI Portugal",
	"PEGI_BBFC": "PEGI BBFC", "RUSSIAN": "Russia", "IARC_GENERIC": "IARC",
}

// count is a counted phrase: key+".one" for one, key with the number otherwise.
func count(lang, key string, n int) string {
	if n == 1 {
		return i18n.T(lang, key+".one")
	}
	return fmt.Sprintf(i18n.T(lang, key), n)
}

// clock is an {hour, minute} object as a 24-hour time.
func clock(v any) string {
	m, _ := v.(map[string]any)
	h, _ := m["hour"].(float64)
	min, _ := m["minute"].(float64)
	return fmt.Sprintf("%02d:%02d", int(h), int(min))
}
