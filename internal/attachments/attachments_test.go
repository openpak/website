package attachments

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSaveOpenServe(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Save("tickets", strings.NewReader("<html>hello</html>"), 100)
	if err != nil || got.Size != 18 || !strings.HasPrefix(got.ContentType, "text/html") {
		t.Fatalf("save: %+v %v", got, err)
	}
	f, err := s.Open(got.Key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "<html>hello</html>" {
		t.Fatalf("read back %q", b)
	}
	// Served as an opaque download, never as a page.
	rec := httptest.NewRecorder()
	s.Serve(rec, httptest.NewRequest("GET", "/", nil), got.Key, `../../evil".html`)
	if rec.Header().Get("Content-Type") != "application/octet-stream" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename=evil.html` || rec.Body.String() != "<html>hello</html>" {
		t.Fatalf("serve: %v %q", rec.Header(), rec.Body.String())
	}

	if _, err := s.Save("crashes", strings.NewReader(strings.Repeat("x", 101)), 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := s.Save("../etc", strings.NewReader("x"), 100); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := s.Open("tickets/../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("traversal: %v", err)
	}
	if s, _ := New(""); s != nil {
		t.Fatal("empty dir must disable attachments")
	}
}
