package pages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/openpak/website/internal/pctl"
)

// The PIN is shown only after the password is given again, and never on a GET.
func TestParentalPinNeedsThePassword(t *testing.T) {
	nx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("account_id") != "acct-a@x.test" {
			w.Write([]byte(`{"devices":[]}`))
			return
		}
		w.Write([]byte(`{"devices":[{"moon_id":"00000000000000aa","paired":true,"unlock_code":"8759","synced":true,"synced_at":1790512000,
			"setting":{"functionalRestrictionLevel":"NONE"},"players":[{"id":"p1","nickname":"Leia"}],"wrong_pin_attempts":[1790511869]}]}`))
	}))
	defer nx.Close()
	s, fc, _ := newTestServer(t)
	s.Parental = pctl.New(nx.URL, "k")
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")

	body := authedRequest(s, a, http.MethodGet, "/account/parental", nil).Body.String()
	if strings.Contains(body, "8759") || !strings.Contains(body, "Leia") || !strings.Contains(body, "Up to date") || !strings.Contains(body, "Wrong PIN entered") {
		t.Fatalf("page: %s", body)
	}
	rec := authedRequest(s, a, http.MethodPost, "/account/parental/pin", &url.Values{"moon_id": {"00000000000000aa"}, "password": {"wrong"}})
	if strings.Contains(rec.Body.String(), "8759") || !strings.Contains(rec.Body.String(), "That password is not right.") {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	rec = authedRequest(s, a, http.MethodPost, "/account/parental/pin", &url.Values{"moon_id": {"00000000000000aa"}, "password": {"password-A-1"}})
	if !strings.Contains(rec.Body.String(), "8759") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("right password: %d", rec.Code)
	}
}

// The setting reads as the app shows it, in its order.
func TestParentalRestrictionsReadLikeTheApp(t *testing.T) {
	got := restrictions("en", map[string]any{
		"functionalRestrictionLevel": "CUSTOM",
		"customSettings": map[string]any{
			"ageRestriction":     map[string]any{"restriction": "RESTRICTED", "ratingAge": float64(3)},
			"ratingOrganization": "PEGI_BBFC",
			"ugcRestriction":     map[string]any{"restriction": "UNRESTRICTED", "applications": map[string]any{"0100B3C014BDA000": map[string]any{}}},
			"snsPostRestriction": "RESTRICTED",
		},
		"playTimerRegulations": map[string]any{
			"timerMode": "DAILY", "restrictionMode": "ALARM",
			"dailyRegulations": map[string]any{
				"timeToPlayInOneDay": map[string]any{"enabled": true, "limitTime": float64(90)},
				"bedtime":            map[string]any{"enabled": true, "startingTime": map[string]any{"hour": float64(21), "minute": float64(0)}},
			},
		},
	})
	want := []fact{
		{"Restriction level", "Custom", ""},
		{"Age limit", "Up to age 3", ""},
		{"Rating system", "PEGI BBFC", ""},
		{"Communicating with others", "Allowed", "1 game set individually"},
		{"Posting to social media", "Restricted", ""},
		{"VR mode", "Not set", ""},
		{"Play time", "90 minutes a day", ""},
		{"Bedtime", "From 21:00", ""},
		{"When time is up", "Alarm only", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

// The editor writes only what the parent changed into the document as served:
// the per-title entries, a bedtime's ending and every other key it does not
// show travel back untouched, and an untouched weekday is not invented.
func TestParentalEditorKeepsWhatItDoesNotShow(t *testing.T) {
	served := map[string]any{
		"functionalRestrictionLevel": "NONE",
		"customSettings": map[string]any{
			"ageRestriction":     map[string]any{"restriction": "UNRESTRICTED", "ratingAge": float64(18)},
			"ugcRestriction":     map[string]any{"restriction": "UNDEFINED", "applications": map[string]any{"0100B3C014BDA000": map[string]any{"restriction": "RESTRICTED"}}},
			"ratingOrganization": "PEGI",
			"vrRestrictionEtag":  "v1",
		},
		"playTimerRegulations": map[string]any{
			"dailyRegulations": map[string]any{"bedtime": map[string]any{"enabled": false, "endingTime": map[string]any{"hour": float64(7), "minute": float64(0)}}},
		},
	}
	form := url.Values{
		"level": {"CUSTOM"}, "age_on": {"0", "1"}, "age": {"12"}, "org": {"PEGI"}, "ugc": {"UNDEFINED"}, "timer": {""},
		"daily.limit_on": {"0", "1"}, "daily.limit": {"90"}, "daily.bed_on": {"0"}, "daily.bed": {""},
		"monday.limit_on": {"0"}, "monday.limit": {""}, "monday.bed_on": {"0"}, "monday.bed": {""},
	}
	got, bad := applyEditor(served, form)
	if bad != nil {
		t.Fatalf("refused %s", bad.name)
	}
	b, _ := json.Marshal(got)
	want := `{"customSettings":{"ageRestriction":{"ratingAge":12,"restriction":"RESTRICTED"},"ratingOrganization":"PEGI",` +
		`"ugcRestriction":{"applications":{"0100B3C014BDA000":{"restriction":"RESTRICTED"}},"restriction":"UNDEFINED"},"vrRestrictionEtag":"v1"},` +
		`"functionalRestrictionLevel":"CUSTOM","playTimerRegulations":{"dailyRegulations":{"bedtime":{"enabled":false,"endingTime":{"hour":7,"minute":0}},` +
		`"timeToPlayInOneDay":{"enabled":true,"limitTime":90}}}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	if _, bad := applyEditor(served, url.Values{"daily.limit": {"361"}}); bad == nil || bad.problem("en") != "Enter a whole number from 0 to 360." {
		t.Fatalf("361 minutes: %v", bad)
	}
	if _, bad := applyEditor(served, url.Values{"daily.limit_on": {"0", "1"}}); bad == nil || bad.name != "daily.limit" {
		t.Fatalf("a limit switched on with no minutes: %v", bad)
	}
}

// nxParentalWrites fakes nx-baas for account A's one console, recording every
// write it takes.
func nxParentalWrites(t *testing.T, writes *[]string) *httptest.Server {
	nx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mine := r.URL.Query().Get("account_id") == "acct-a@x.test"
		switch {
		case r.URL.Path == "/internal/pctl/devices":
			if !mine {
				w.Write([]byte(`{"devices":[]}`))
				return
			}
			w.Write([]byte(`{"devices":[{"moon_id":"00000000000000aa","paired":true,"synced":false,"nickname":"Switch ···0001","serial_number":"XAW10000000001","firmware":"22.5.0",
				"setting":{"functionalRestrictionLevel":"NONE","customSettings":{"ugcRestriction":{"applications":{"0100E65002BB8000":{"restriction":"RESTRICTED"}}}}}}]}`))
		case r.URL.Path == "/internal/pctl/devices/00000000000000aa/activity" && mine:
			w.Write([]byte(`{"days":[{"date":"2026-09-27","total_minutes":95,"titles":[{"title_id":"0100e65002bb8000","minutes":60}]}]}`))
		case strings.HasPrefix(r.URL.Path, "/internal/pctl/devices/00000000000000aa") && mine && r.Method != http.MethodGet:
			body := map[string]any{}
			json.NewDecoder(r.Body).Decode(&body)
			enc, _ := json.Marshal(body)
			*writes = append(*writes, r.Method+" "+string(enc))
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Write([]byte(`{"etag":"e2","updated_at":9}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	t.Cleanup(nx.Close)
	return nx
}

// Saving, changing the PIN and removing ask for the password; nothing is written
// without it, for another account's console, or over a document changed
// since the form was drawn.
func TestParentalWritesNeedThePasswordAndTheConsole(t *testing.T) {
	var writes []string
	s, fc, _ := newTestServer(t)
	s.Parental = pctl.New(nxParentalWrites(t, &writes).URL, "k")
	emailA, emailB := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")

	page := authedRequest(s, a, http.MethodGet, "/account/parental", nil).Body.String()
	for _, want := range []string{"Change restrictions", "Stardew Valley: Restricted", "Time used", "60 minutes", "Remove console"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q", want)
		}
	}
	was := settingHash(map[string]any{"functionalRestrictionLevel": "NONE", "customSettings": map[string]any{"ugcRestriction": map[string]any{
		"applications": map[string]any{"0100E65002BB8000": map[string]any{"restriction": "RESTRICTED"}}}}})
	edit := func(password, was string) *httptest.ResponseRecorder {
		return authedRequest(s, a, http.MethodPost, "/account/parental/setting",
			&url.Values{"moon_id": {"00000000000000aa"}, "was": {was}, "level": {"CHILDREN"}, "password": {password}})
	}
	if rec := edit("wrong", was); !strings.Contains(rec.Body.String(), "That password is not right.") || len(writes) != 0 {
		t.Fatalf("wrong password: %d %v", rec.Code, writes)
	}
	if rec := edit("password-A-1", "stale"); !strings.Contains(rec.Body.String(), "changed while you were editing") || len(writes) != 0 {
		t.Fatalf("stale form: %d %v", rec.Code, writes)
	}
	rec := edit("password-A-1", was)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/account/parental?saved=00000000000000aa") ||
		len(writes) != 1 || !strings.Contains(writes[0], `"functionalRestrictionLevel":"CHILDREN"`) || !strings.Contains(writes[0], "0100E65002BB8000") {
		t.Fatalf("save: %d %v", rec.Code, writes)
	}
	next, _, _ := strings.Cut(rec.Header().Get("Location"), "#") // a browser never sends the fragment
	if page := authedRequest(s, a, http.MethodGet, next, nil).Body.String(); !strings.Contains(page, "Saved. Waiting for the console.") {
		t.Fatalf("no waiting notice after a save: %s", page)
	}

	pin := func(pin, again string) *httptest.ResponseRecorder {
		return authedRequest(s, a, http.MethodPost, "/account/parental/pin/set",
			&url.Values{"moon_id": {"00000000000000aa"}, "new_pin": {pin}, "new_pin_again": {again}, "password": {"password-A-1"}})
	}
	if rec := pin("1234", "1243"); !strings.Contains(rec.Body.String(), "The two PINs do not match.") || len(writes) != 1 {
		t.Fatalf("mismatch: %v", writes)
	}
	if rec := pin("12345", "12345"); !strings.Contains(rec.Body.String(), "The PIN must be four digits.") || len(writes) != 1 {
		t.Fatalf("five digits: %v", writes)
	}
	if rec := pin("0427", "0427"); rec.Code != http.StatusSeeOther || len(writes) != 2 || writes[1] != `PUT {"unlock_code":"0427"}` {
		t.Fatalf("pin: %d %v", rec.Code, writes)
	}
	remove := func(password string) *httptest.ResponseRecorder {
		return authedRequest(s, a, http.MethodPost, "/account/parental/remove", &url.Values{"moon_id": {"00000000000000aa"}, "password": {password}})
	}
	for _, password := range []string{"", "wrong"} {
		if rec := remove(password); !strings.Contains(rec.Body.String(), "That password is not right.") || len(writes) != 2 {
			t.Fatalf("remove with password %q: %d %v", password, rec.Code, writes)
		}
	}
	if rec := remove("password-A-1"); rec.Code != http.StatusSeeOther || len(writes) != 3 || writes[2] != "DELETE {}" {
		t.Fatalf("remove: %d %v", rec.Code, writes)
	}

	if !strings.Contains(page, "Switch ···0001") || !strings.Contains(page, "Serial number XAW10000000001") || !strings.Contains(page, "System version 22.5.0") {
		t.Fatal("page lacks the console's name, serial or version")
	}
	name := func(n string) *httptest.ResponseRecorder {
		return authedRequest(s, a, http.MethodPost, "/account/parental/name", &url.Values{"moon_id": {"00000000000000aa"}, "name": {n}})
	}
	if rec := name(strings.Repeat("a", 33)); !strings.Contains(rec.Body.String(), "A name can be up to 32 characters") || len(writes) != 3 {
		t.Fatalf("33 characters: %v", writes)
	}
	if rec := name(" Living room "); rec.Code != http.StatusSeeOther || len(writes) != 4 || writes[3] != `PUT {"name":"Living room"}` {
		t.Fatalf("rename without a password: %d %v", rec.Code, writes)
	}

	b := login(t, s, emailB, "password-B-1")
	for _, c := range []struct {
		path string
		form url.Values
	}{
		{"/account/parental/setting", url.Values{"moon_id": {"00000000000000aa"}, "was": {was}, "level": {"NONE"}, "password": {"password-B-1"}}},
		{"/account/parental/pin/set", url.Values{"moon_id": {"00000000000000aa"}, "new_pin": {"1111"}, "new_pin_again": {"1111"}, "password": {"password-B-1"}}},
		{"/account/parental/remove", url.Values{"moon_id": {"00000000000000aa"}, "password": {"password-B-1"}}},
		{"/account/parental/name", url.Values{"moon_id": {"00000000000000aa"}, "name": {"Mine now"}}},
	} {
		if rec := authedRequest(s, b, http.MethodPost, c.path, &c.form); rec.Code != http.StatusNotFound {
			t.Errorf("%s as another account: %d", c.path, rec.Code)
		}
	}
	if len(writes) != 4 {
		t.Fatalf("another account wrote: %v", writes)
	}
}
