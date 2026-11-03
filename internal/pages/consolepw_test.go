package pages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/consolesync"
)

// fakeNN stands in for nn-account: it records what the website hands it.
type fakeNN struct {
	mu    sync.Mutex
	calls []map[string]string
	code  int // answer; 0 is 200
	mode  string
}

func (f *fakeNN) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Internal-Key") != "k" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /internal/wiiu/console-password":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.calls = append(f.calls, in)
		code := f.code
		f.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": in["mode"], "nnid": "playera"})
	case "GET /internal/wiiu/console-password":
		json.NewEncoder(w).Encode(map[string]any{"nnid": "playera", "pid": 1, "mode": f.mode, "min": 6, "max": 16})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeNN) last(t *testing.T) map[string]string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("nn-account was not called")
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeNN) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func syncServer(t *testing.T) (*Server, *fakeCore, *fakeNN, *consolesync.HTTP) {
	t.Helper()
	s, fc, _ := newTestServer(t)
	nn := &fakeNN{mode: "sync"}
	ts := httptest.NewServer(nn)
	t.Cleanup(ts.Close)
	s.NNAdapter = adapter.New(ts.URL, "k")
	w := consolesync.New(fc, s.NNAdapter, discardLogger()).(*consolesync.HTTP)
	s.Core = w
	return s, fc, nn, w
}

// Signing in hands the OpenPak password to nn-account, so a Wii U accepts it too.
func TestSignInSyncsConsolePassword(t *testing.T) {
	s, fc, nn, w := syncServer(t)
	emailA, _ := seedTwoAccounts(fc)
	login(t, s, emailA, "password-A-1")
	w.Wait.Wait()
	got := nn.last(t)
	if got["account_id"] != "acct-"+emailA || got["password"] != "password-A-1" || got["mode"] != "sync" {
		t.Fatalf("sync call: %v", got)
	}
}

// A 2FA account syncs once the second factor completes the sign-in, not before.
func TestTwoFactorSignInSyncsAfterTheCode(t *testing.T) {
	s, fc, nn, w := syncServer(t)
	emailA, _ := seedTwoAccounts(fc)
	fc.twoFactor[emailA] = true
	mux := s.Handler()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login", url.Values{"email": {emailA}, "password": {"password-A-1"}}))
	w.Wait.Wait()
	if nn.count() != 0 {
		t.Fatal("synced before the second factor")
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, post("/login/2fa", url.Values{"challenge": {"ch-" + emailA}, "code": {"123456"}}))
	w.Wait.Wait()
	if rec.Code != http.StatusSeeOther || nn.last(t)["password"] != "password-A-1" {
		t.Fatalf("after code: %d %v", rec.Code, nn.calls)
	}
}

// A wrong password never reaches nn-account.
func TestFailedSignInDoesNotSync(t *testing.T) {
	s, fc, nn, w := syncServer(t)
	emailA, _ := seedTwoAccounts(fc)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, post("/login", url.Values{"email": {emailA}, "password": {"nope-nope-1"}}))
	w.Wait.Wait()
	if nn.count() != 0 {
		t.Fatal("a failed sign-in was synced")
	}
}

// A password change moves the console password with it.
func TestPasswordChangeSyncsConsolePassword(t *testing.T) {
	s, fc, nn, w := syncServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")
	w.Wait.Wait()
	rec := authedRequest(s, a, http.MethodPost, "/account/password", &url.Values{
		"current_password": {"password-A-1"}, "new_password": {"brand-new-pass-2"}, "confirm_password": {"brand-new-pass-2"}})
	w.Wait.Wait()
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "password-changed") {
		t.Fatalf("change: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if nn.last(t)["password"] != "brand-new-pass-2" || nn.count() != 2 {
		t.Fatalf("calls: %v", nn.calls)
	}
}

// A dead nn-account never fails or delays a sign-in.
func TestSignInSurvivesNNAccountFailure(t *testing.T) {
	s, fc, nn, w := syncServer(t)
	nn.code = http.StatusInternalServerError
	emailA, _ := seedTwoAccounts(fc)
	login(t, s, emailA, "password-A-1")
	w.Wait.Wait()
	if nn.count() != 1 {
		t.Fatalf("calls: %d", nn.count())
	}
}

// The separate console password asks for the OpenPak password again, and says what it set.
func TestSeparateConsolePasswordNeedsCurrentPassword(t *testing.T) {
	s, fc, nn, w := syncServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")
	w.Wait.Wait()
	before := nn.count()

	for name, form := range map[string]url.Values{
		"wrong current": {"console_password": {"consolePass9"}, "confirm_console_password": {"consolePass9"}, "password": {"wrong-wrong-1"}},
		"mismatch":      {"console_password": {"consolePass9"}, "confirm_console_password": {"consolePass8"}, "password": {"password-A-1"}},
	} {
		f := form
		rec := authedRequest(s, a, http.MethodPost, "/account/console-password", &f)
		if rec.Code != http.StatusSeeOther || nn.count() != before {
			t.Fatalf("%s: %d, calls %d", name, rec.Code, nn.count())
		}
	}
	rec := authedRequest(s, a, http.MethodPost, "/account/console-password", &url.Values{
		"console_password": {"consolePass9"}, "confirm_console_password": {"consolePass9"}, "password": {"password-A-1"}})
	if !strings.Contains(rec.Header().Get("Location"), "console-saved") {
		t.Fatalf("saved: %s", rec.Header().Get("Location"))
	}
	if got := nn.last(t); got["mode"] != "custom" || got["password"] != "consolePass9" {
		t.Fatalf("call: %v", got)
	}

	// The account page names the ID to type.
	page := authedRequest(s, a, http.MethodGet, "/account", nil)
	if !strings.Contains(page.Body.String(), "<code>playera</code>") || !strings.Contains(page.Body.String(), "OpenPak password") {
		t.Fatalf("account page lacks the console ID row: %d", page.Code)
	}
}

// TestWriteConsolePreview writes the account page in each console-password state, for a human
// looking at it; it runs only when PREVIEW_DIR is set (see TestWritePreview).
func TestWriteConsolePreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to write the previews")
	}
	t.Chdir("../..")
	s, fc, nn, w := syncServer(t)
	emailA, _ := seedTwoAccounts(fc)
	a := login(t, s, emailA, "password-A-1")
	w.Wait.Wait()
	_ = os.MkdirAll(dir, 0o755)
	for _, mode := range []string{"sync", "", "incompatible", "custom"} {
		nn.mode = mode
		name := mode
		if name == "" {
			name = "unsynced"
		}
		rec := authedRequest(s, a, http.MethodGet, "/account", nil)
		if err := os.WriteFile(filepath.Join(dir, "account-console-"+name+".html"), rec.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
