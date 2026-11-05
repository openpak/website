package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openpak/website/internal/rewards/rewardstest"
	"github.com/openpak/website/internal/store"
)

// TestWritePreview renders the admin pages to HTML files, like the pages
// package's preview. It runs only when PREVIEW_DIR is set.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to write the page previews")
	}
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin"})
	st := rewardstest.NewState()
	s.Rewards = rewardstest.New(t, st)
	s.Logs = sampleLogs()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	for _, p := range []string{"", "monitor", "services", "news", "titles", "users", "tickets", "crashes", "config", "events", "rewards", "rewards?all=1&m=resolved", "rewards?down", "logs?service=openpak-account", "logs?service=openpak-account&level=2"} {
		st.Down = p == "rewards?down"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, strings.TrimSuffix("/admin/"+strings.Replace(p, "?down", "", 1), "/"), nil))
		html := strings.ReplaceAll(rec.Body.String(), `"/static/`, `"static/`)
		name := "admin"
		if p != "" {
			name += "-" + strings.NewReplacer("?", "-", "&", "-", "=", "-").Replace(p)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
