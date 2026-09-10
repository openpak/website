package pages

import (
	"bytes"
	"testing"

	"github.com/openpak/website/internal/store"
)

// The /titles page 500'd once because a template used .Lang inside the
// {{with .Titles}} scope, where the dot is the slice, not baseData. Render it
// for real so that class of error fails a test, not production.
func TestTitlesPageRenders(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	type titlesPage struct {
		baseData
		Titles   []store.Title
		Consoles []string
		Statuses []string
	}
	pg := titlesPage{
		baseData{Lang: "en", Path: "/titles", Title: "Supported titles"},
		[]store.Title{{Name: "Among Us", Platform: "Switch", Status: "alpha"}},
		[]string{"Switch"}, []string{"alpha", "beta"},
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "titles.html", pg); err != nil {
		t.Fatalf("titles.html failed to render: %v", err)
	}
	for _, want := range []string{"titles-console", "titles-status", "Among Us", "/static/titles.js"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("titles.html missing %q", want)
		}
	}
}
