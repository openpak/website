package pages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/accounts"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/pctl"
	"github.com/openpak/website/internal/store"
)

// Parental controls, the web half of the app's screen: the same fields, in the
// same order, with the same words. The PIN is behind the account password
// (and the second factor, where the account has one) on every reveal, and is
// never in a URL: the reveal is a POST whose answer is the page itself. Saving
// restrictions and changing the PIN ask for the password the same way.

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

	EditFor     string         // the console whose editor is open
	EditDoc     map[string]any // what that editor shows instead of the served document
	EditError   string
	Saved       string // the console whose restrictions were just saved
	NewPinOpen  string // the console whose change-PIN form is open
	NewPinError string
	PinSaved    string // the console that was just given a new PIN
	RemoveOpen  string // the console whose removal is being confirmed
	RemoveError string
	Removed     bool
}

type consoleView struct {
	pctl.Device
	Name         string
	LastSynced   string
	Restrictions []fact
	WrongPins    []string
	Editor       editorView
	Activity     []dayView
}

type dayView struct {
	Date, Total string
	Titles      []fact
}

type fact struct{ Label, Value, Note string }

func (s *Server) handleParental(w http.ResponseWriter, r *http.Request) {
	v := s.requireViewer(w, r)
	if v == nil {
		return
	}
	q := r.URL.Query()
	s.renderParental(w, r, v, parentalView{PinOpen: q.Get("pin"), NewPinOpen: q.Get("newpin"), RemoveOpen: q.Get("remove"),
		Saved: q.Get("saved"), PinSaved: q.Get("pinset"), Removed: q.Has("removed")})
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
	var catalogue map[string]string
	for i, d := range devices {
		if !d.Paired && d.PairingCode != "" && view.Code == nil {
			view.Code = &devices[i]
		}
		if !d.Paired {
			continue
		}
		if catalogue == nil {
			catalogue = TitleCatalogue(r.Context(), s.DB)
		}
		c := consoleOf(lang, d)
		doc := d.Setting
		if view.EditFor == d.MoonID && view.EditDoc != nil {
			doc = view.EditDoc
		}
		c.Editor = editorOf(lang, doc, catalogue)
		c.Editor.Was = settingHash(d.Setting)
		days, err := s.Parental.Activity(r.Context(), v.AccountID, d.MoonID, 7)
		if err != nil {
			s.log.Error("parental activity", "err", err)
		}
		nameTitles(catalogue, days)
		c.Activity = activityOf(lang, days)
		view.Consoles = append(view.Consoles, c)
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

// stepUp asks for the password again, and the second factor where there is
// one: a signed-in browser someone else is sitting at must not be enough. msg
// is what to tell a parent who got it wrong; ok is false when the answer has
// already been written.
func (s *Server) stepUp(w http.ResponseWriter, r *http.Request, v *viewer) (msg string, ok bool) {
	if !s.AuthLimit.Allow(clientKey(r)) {
		http.Error(w, "too many attempts; slow down", http.StatusTooManyRequests)
		return "", false
	}
	lang := i18n.FromRequest(r)
	err := accounts.Reauthenticate(r.Context(), s.Identity, v.AccountID, r.PostFormValue("password"), r.PostFormValue("code"))
	switch {
	case errors.Is(err, core.ErrInvalidCredentials), errors.Is(err, core.ErrSecondFactorRequired), errors.Is(err, core.ErrInvalidCode):
		return i18n.T(lang, "parental.pin.wrong"), true
	case errors.Is(err, core.ErrTwoFactorLocked):
		return i18n.T(lang, "auth.2fa.locked"), true
	case err != nil:
		s.log.Error("parental step-up", "err", err)
		http.Error(w, "the account service did not answer", http.StatusBadGateway)
		return "", false
	}
	return "", true
}

// handleParentalPin reveals one console's PIN once the password has been given
// again.
func (s *Server) handleParentalPin(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	lang := i18n.FromRequest(r)
	moon := r.PostFormValue("moon_id")
	view := parentalView{PinOpen: moon}
	msg, ok := s.stepUp(w, r, v)
	if !ok {
		return
	}
	if msg != "" {
		view.PinError = msg
	} else {
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

// handleParentalSetPin gives the console a new PIN, which it takes at its next
// sync. Asked twice, because a PIN mistyped here is a console nobody can
// unlock; never logged.
func (s *Server) handleParentalSetPin(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	lang := i18n.FromRequest(r)
	moon, pin := r.PostFormValue("moon_id"), r.PostFormValue("new_pin")
	view := parentalView{NewPinOpen: moon}
	switch {
	case !pctl.ValidPin(pin):
		view.NewPinError = i18n.T(lang, "parental.pin.invalid")
	case pin != r.PostFormValue("new_pin_again"):
		view.NewPinError = i18n.T(lang, "parental.pin.mismatch")
	default:
		msg, ok := s.stepUp(w, r, v)
		if !ok {
			return
		}
		if msg != "" {
			view.NewPinError = msg
			break
		}
		if err := s.Parental.SetUnlockCode(r.Context(), v.AccountID, moon, pin); err != nil {
			parentalWriteErr(w, r, s, err)
			return
		}
		http.Redirect(w, r, "/account/parental?pinset="+url.QueryEscape(moon)+"#pin-"+url.QueryEscape(moon), http.StatusSeeOther)
		return
	}
	s.renderParental(w, r, v, view)
}

// handleParentalSetting saves the editor. The document sent is the one served,
// with only the controls the parent changed written into it, so keys the
// editor does not show -- the per-title entries a console reports, above all --
// are never dropped. nx-baas refuses what the console could not parse, and that
// refusal is shown as it is, never worked around.
func (s *Server) handleParentalSetting(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	lang := i18n.FromRequest(r)
	moon := r.PostFormValue("moon_id")
	devices, err := s.Parental.Devices(r.Context(), v.AccountID)
	if err != nil {
		parentalWriteErr(w, r, s, err)
		return
	}
	i := slices.IndexFunc(devices, func(d pctl.Device) bool { return d.MoonID == moon && d.Paired })
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	served := devices[i].Setting
	doc, bad := applyEditor(served, r.PostForm)
	view := parentalView{EditFor: moon, EditDoc: doc}
	switch {
	case bad != nil:
		view.EditError = bad.problem(lang)
	case settingHash(served) != r.PostFormValue("was"):
		// Changed from the app since this form was drawn: saving over it would
		// undo that change with values the parent never touched here.
		view.EditDoc, view.EditError = nil, i18n.T(lang, "parental.edit.stale")
	default:
		msg, ok := s.stepUp(w, r, v)
		if !ok {
			return
		}
		if msg != "" {
			view.EditError = msg
			break
		}
		_, err := s.Parental.SetSetting(r.Context(), v.AccountID, moon, doc)
		var pe *pctl.Error
		if errors.As(err, &pe) && pe.Path() != "" {
			view.EditError = fmt.Sprintf(i18n.T(lang, "parental.edit.refused"), pe.Path())
			break
		}
		if err != nil {
			parentalWriteErr(w, r, s, err)
			return
		}
		http.Redirect(w, r, "/account/parental?saved="+url.QueryEscape(moon)+"#c-"+url.QueryEscape(moon), http.StatusSeeOther)
		return
	}
	s.renderParental(w, r, v, view)
}

// handleParentalRemove takes a console off this account's list, once the
// parent has read what that does and does not do and given the password again.
func (s *Server) handleParentalRemove(w http.ResponseWriter, r *http.Request) {
	v, ok := s.postGuard(w, r)
	if !ok {
		return
	}
	moon := r.PostFormValue("moon_id")
	msg, ok := s.stepUp(w, r, v)
	if !ok {
		return
	}
	if msg != "" {
		s.renderParental(w, r, v, parentalView{RemoveOpen: moon, RemoveError: msg})
		return
	}
	if err := s.Parental.Remove(r.Context(), v.AccountID, moon); err != nil {
		parentalWriteErr(w, r, s, err)
		return
	}
	http.Redirect(w, r, "/account/parental?removed=1", http.StatusSeeOther)
}

// parentalWriteErr answers a write nx-baas did not take: a console that is not
// this account's is simply not found.
func parentalWriteErr(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	var pe *pctl.Error
	if errors.As(err, &pe) && pe.Status == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	s.log.Error("parental write", "err", err)
	http.Error(w, "the parental controls service did not answer", http.StatusBadGateway)
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

// editorField is one control of the restrictions editor and the key of the
// setting document it writes (the console's parse table, docs study §5b).
// kind: "e" one of opts, "r" a checkbox that is RESTRICTED or UNRESTRICTED,
// "b" a checkbox, "i<max>" a whole number from 0 to max (the console's own
// bound), "t" a time of day {hour, minute}.
type editorField struct {
	name, path, kind string
	opts             []string
}

// editorDays are the play-timer blocks: the one for every day, then one per
// weekday under the console's own day names.
var editorDays = []string{"daily", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

var editorFields = func() []editorField {
	restriction := []string{"UNRESTRICTED", "RESTRICTED"}
	fs := []editorField{
		{"level", "functionalRestrictionLevel", "e", []string{"NONE", "CHILDREN", "YOUNG_TEENS", "OLDER_TEENS", "CUSTOM"}},
		{"age_on", "customSettings.ageRestriction.restriction", "r", nil},
		{"age", "customSettings.ageRestriction.ratingAge", "i255", nil},
		{"org", "customSettings.ratingOrganization", "e", []string{"CERO", "GRAC_GCRB", "GSRMR", "ESRB", "CLASS_IND", "USK", "PEGI",
			"PEGI_PORTUGAL", "PEGI_BBFC", "RUSSIAN", "ACB", "OFLC", "IARC_GENERIC", "GMEDIA"}},
		{"ugc", "customSettings.ugcRestriction.restriction", "e", restriction},
		{"sns", "customSettings.snsPostRestriction", "e", restriction},
		{"vr", "customSettings.vrRestriction", "e", restriction},
		{"timer", "playTimerRegulations.timerMode", "e", []string{"DAILY", "EACH_DAY_OF_THE_WEEK"}},
	}
	for _, d := range editorDays {
		base := "playTimerRegulations.eachDayOfTheWeekRegulations." + d
		if d == "daily" {
			base = "playTimerRegulations.dailyRegulations"
		}
		fs = append(fs,
			editorField{d + ".limit_on", base + ".timeToPlayInOneDay.enabled", "b", nil},
			editorField{d + ".limit", base + ".timeToPlayInOneDay.limitTime", "i360", nil},
			editorField{d + ".bed_on", base + ".bedtime.enabled", "b", nil},
			editorField{d + ".bed", base + ".bedtime.startingTime", "t", nil})
	}
	return append(fs, editorField{"mode", "playTimerRegulations.restrictionMode", "e", []string{"ALARM", "FORCED_TERMINATION"}})
}()

// problem is what the parent is told about a value this control will not take.
func (f editorField) problem(lang string) string {
	if max, err := strconv.Atoi(strings.TrimPrefix(f.kind, "i")); err == nil {
		return fmt.Sprintf(i18n.T(lang, "parental.edit.number"), max)
	}
	return fmt.Sprintf(i18n.T(lang, "parental.edit.refused"), f.path)
}

// current is the control's value in doc as the form spells it: "" for a key
// the document does not have.
func (f editorField) current(doc map[string]any) string {
	v := docGet(doc, f.path)
	switch f.kind[0] {
	case 'r':
		return map[bool]string{true: "1", false: "0"}[v == "RESTRICTED"]
	case 'b':
		return map[bool]string{true: "1", false: "0"}[v == true]
	case 'i':
		if n, ok := v.(float64); ok {
			return strconv.Itoa(int(n))
		}
		return ""
	case 't':
		if _, ok := v.(map[string]any); ok {
			return clock(v)
		}
		return ""
	}
	s, _ := v.(string)
	return s
}

// applyEditor is served with each control the parent changed written in. A
// control whose value is what served already says is left alone, so a key
// the document lacks stays absent. bad is the control whose value cannot be
// sent.
func applyEditor(served map[string]any, form url.Values) (doc map[string]any, bad *editorField) {
	b, _ := json.Marshal(served)
	_ = json.Unmarshal(b, &doc)
	if doc == nil {
		doc = map[string]any{}
	}
	for i, f := range editorFields {
		vals := form[f.name]
		if len(vals) == 0 {
			continue
		}
		// A checkbox is sent after a hidden "0" of the same name, so the last
		// value is the box's.
		sub := strings.TrimSpace(vals[len(vals)-1])
		if sub == f.current(served) || (sub == "" && f.kind[0] != 'e') {
			continue
		}
		var v any
		switch f.kind[0] {
		case 'e':
			v = sub
		case 'r':
			v = map[bool]string{true: "RESTRICTED", false: "UNRESTRICTED"}[sub == "1"]
		case 'b':
			v = sub == "1"
		case 'i':
			n, err := strconv.Atoi(sub)
			if max, _ := strconv.Atoi(f.kind[1:]); err != nil || n < 0 || n > max {
				return doc, &editorFields[i]
			}
			v = n
		case 't':
			t, err := time.Parse("15:04", sub)
			if err != nil {
				return doc, &editorFields[i]
			}
			v = map[string]any{"hour": t.Hour(), "minute": t.Minute()}
		}
		docSet(doc, f.path, v)
	}
	// A limit or a bedtime switched on here needs its number: the console's
	// own table makes both optional, and what it does without them is unknown.
	for i, f := range editorFields {
		if f.kind[0] == 'b' && docGet(doc, f.path) == true && f.current(served) != "1" {
			need := editorFields[i+1]
			if docGet(doc, need.path) == nil {
				return doc, &editorFields[i+1]
			}
		}
	}
	return doc, nil
}

func docGet(doc map[string]any, path string) any {
	var v any = doc
	for _, k := range strings.Split(path, ".") {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

func docSet(doc map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := doc[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			doc[k] = next
		}
		doc = next
	}
	doc[keys[len(keys)-1]] = v
}

// settingHash names a served document, so a save can tell the document it
// edits from one changed elsewhere since the form was drawn.
func settingHash(doc map[string]any) string {
	b, _ := json.Marshal(doc)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// editorView is the form: V holds each control's value, Opts each enum's
// options, and the per-title lists are shown as they are.
type editorView struct {
	V          map[string]string
	Opts       map[string][]option
	Days       []fact // Value a play-timer block's control prefix, Label its day
	Exceptions []fact
	Whitelist  []fact
	Was        string
}

type option struct {
	Value, Label string
	Selected     bool
}

func editorOf(lang string, doc map[string]any, catalogue map[string]string) editorView {
	e := editorView{V: map[string]string{}, Opts: map[string][]option{}}
	for _, f := range editorFields {
		cur := f.current(doc)
		e.V[f.name] = cur
		if f.kind != "e" {
			continue
		}
		var opts []option
		if !slices.Contains(f.opts, cur) {
			// UNDEFINED, or nothing: shown as not set, and kept unless changed.
			opts = append(opts, option{cur, i18n.T(lang, "parental.r.unset"), true})
		}
		for _, o := range f.opts {
			opts = append(opts, option{o, optionLabel(lang, f.name, o), o == cur})
		}
		e.Opts[f.name] = opts
	}
	for _, d := range editorDays {
		e.Days = append(e.Days, fact{Label: i18n.T(lang, "parental.day."+d), Value: d})
	}
	titles := func(m map[string]any, key string, word func(string) string) []fact {
		var out []fact
		for id, v := range m {
			entry, _ := v.(map[string]any)
			value, _ := entry[key].(string)
			out = append(out, fact{Label: titleOr(catalogue, id), Value: word(value)})
		}
		slices.SortFunc(out, func(a, b fact) int { return strings.Compare(a.Label, b.Label) })
		return out
	}
	apps, _ := docGet(doc, "customSettings.ugcRestriction.applications").(map[string]any)
	e.Exceptions = titles(apps, "restriction", func(v string) string { return optionLabel(lang, "ugc", v) })
	white, _ := doc["whitelistedApplications"].(map[string]any)
	e.Whitelist = titles(white, "safeLaunch", func(v string) string {
		return optionLabel(lang, "ugc", map[string]string{"ALLOW": "UNRESTRICTED"}[v])
	})
	return e
}

func optionLabel(lang, field, v string) string {
	switch field {
	case "level":
		return i18n.T(lang, "parental.level."+v)
	case "org":
		if name, ok := ratingOrgs[v]; ok {
			return name
		}
		return v
	case "timer":
		return i18n.T(lang, "parental.timer."+v)
	case "mode":
		return i18n.T(lang, "parental.timeup."+v)
	}
	if v == "RESTRICTED" || v == "UNRESTRICTED" {
		return i18n.T(lang, "parental.r."+v)
	}
	return i18n.T(lang, "parental.r.unset")
}

// titleOr is a title's name from the titles catalogue, or its id.
func titleOr(catalogue map[string]string, id string) string {
	if n, _ := FriendGame(context.Background(), catalogue, "", id); n != "" {
		return n
	}
	return id
}

// nameTitles names each title in the activity from the titles catalogue (no
// live lookup: a list of dozens must not wait on the network). A title it does
// not know is left without a name.
func nameTitles(catalogue map[string]string, days []pctl.Day) {
	for i := range days {
		for j, t := range days[i].Titles {
			days[i].Titles[j].Name, _ = FriendGame(context.Background(), catalogue, "", t.TitleID)
		}
	}
}

// NameTitles is nameTitles for the app's API.
func NameTitles(ctx context.Context, db store.Store, days []pctl.Day) {
	if len(days) > 0 {
		nameTitles(TitleCatalogue(ctx, db), days)
	}
}

func activityOf(lang string, days []pctl.Day) []dayView {
	var out []dayView
	for _, d := range days {
		date := d.Date
		if t, err := time.Parse("2006-01-02", d.Date); err == nil {
			date = i18n.Date(lang, t)
		}
		// Time used is the console's own count, HOME menu included: it can be
		// more than the titles add up to, and a day can have time and no title.
		if d.TotalMinutes == 0 {
			continue
		}
		dv := dayView{Date: date, Total: count(lang, "parental.minutes", d.TotalMinutes)}
		for _, t := range d.Titles {
			name := t.Name
			if name == "" {
				name = t.TitleID
			}
			dv.Titles = append(dv.Titles, fact{Label: name, Value: count(lang, "parental.minutes", t.Minutes)})
		}
		out = append(out, dv)
	}
	return out
}
