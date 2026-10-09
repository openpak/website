package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root"})
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	for _, p := range []string{"", "monitor", "services", "news", "titles", "users", "tickets", "crashes", "config", "events"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, strings.TrimSuffix("/admin/"+p, "/"), nil))
		html := strings.ReplaceAll(rec.Body.String(), `"/static/`, `"static/`)
		name := "admin"
		if p != "" {
			name += "-" + p
		}
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
