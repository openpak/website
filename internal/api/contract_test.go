package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The app's client is generated from openapi.yaml, so a route that exists only
// in the code is a route the app cannot call, and a path documented but never
// served is a promise nobody keeps. Rather than a hand-kept list that drifts
// with everything else, the route table is read from the source that defines it.
//
// ponytail: a regex over the package's route registrations and a substring search of the spec, not
// a YAML parser and not a new dependency. If the spec ever needs real schema
// validation, that is the moment to add one.
func TestEveryRouteIsInTheOpenAPISpec(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var src []byte
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, b...)
	}
	spec, err := os.ReadFile("../../openapi/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// h("/path"), g("/path"), d("/path") through the route helpers, and
	// mux.HandleFunc("METHOD /api/v1/path") registered straight on the mux.
	// /api/v2 and /internal are not the app's API and have no place in this spec.
	helper := regexp.MustCompile(`\b[hgd]\("(/[^"]*)"`).FindAllStringSubmatch(string(src), -1)
	direct := regexp.MustCompile(`HandleFunc\("[A-Z]+ /api/v1(/[^"]*)"`).FindAllStringSubmatch(string(src), -1)
	routes := append(helper, direct...)
	if len(helper) < 10 || len(direct) < 10 {
		t.Fatalf("found %d helper and %d direct routes; the regexes have lost track", len(helper), len(direct))
	}
	for _, m := range routes {
		path := m[1]
		if !strings.Contains(string(spec), "\n  "+path+":") {
			t.Errorf("%s is served but missing from openapi.yaml", path)
		}
	}
}
