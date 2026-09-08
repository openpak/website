package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The app's client is generated from openapi.yaml, so a route that exists only
// in the code is a route the app cannot call, and a path documented but never
// served is a promise nobody keeps. Rather than a hand-kept list that drifts
// with everything else, the route table is read from the source that defines it.
//
// ponytail: a regex over RegisterRoutes and a substring search of the spec, not
// a YAML parser and not a new dependency. If the spec ever needs real schema
// validation, that is the moment to add one.
func TestEveryRouteIsInTheOpenAPISpec(t *testing.T) {
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../openapi/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// h("/path"), g("/path"), d("/path") inside RegisterRoutes.
	routes := regexp.MustCompile(`\b[hgd]\("(/[^"]*)"`).FindAllStringSubmatch(string(src), -1)
	if len(routes) < 10 {
		t.Fatalf("found %d routes; the regex has lost track of RegisterRoutes", len(routes))
	}
	for _, m := range routes {
		path := m[1]
		if !strings.Contains(string(spec), "\n  "+path+":") {
			t.Errorf("%s is served but missing from openapi.yaml", path)
		}
	}
}
