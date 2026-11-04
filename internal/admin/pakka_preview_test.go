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

// TestWritePakkaPreview renders the Pakka pages to HTML files for screenshots; PREVIEW_DIR only.
func TestWritePakkaPreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to write the page previews")
	}
	s, db, _ := newAdmin(t)
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin", DisplayName: "Root"})
	fp := newFakePakka()
	srv := httptest.NewServer(fp)
	defer srv.Close()
	s.Pakka = NewPakkaAPI(srv.URL, "pk-admin")
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	for name, p := range map[string]string{"queue": "/admin/pakka", "draft": "/admin/pakka/a1b2c3d4", "template": "/admin/pakka/99887766", "news": "/admin/pakka/e5f6a7b8"} {
		rec := get(mux, p)
		html := strings.ReplaceAll(rec.Body.String(), `"/static/`, `"static/`)
		if err := os.WriteFile(filepath.Join(dir, "pakka-"+name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
