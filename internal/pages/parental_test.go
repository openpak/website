package pages

import (
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
