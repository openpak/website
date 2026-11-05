package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientUpdateRelay(t *testing.T) {
	hits := 0
	var gh *httptest.Server
	gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/openpak/ps3-patcher/releases/latest":
			hits++
			fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":"manifest.txt","browser_download_url":"%s/a/manifest.txt"},{"name":"manifest.sig","browser_download_url":"%s/a/manifest.sig"},{"name":"OpenPak-PS3-Patcher-v1.2.3.pkg","browser_download_url":"%s/a/pkg"}]}`, gh.URL, gh.URL, gh.URL)
		case "/a/manifest.txt":
			fmt.Fprint(w, "version=1.2.3\n")
		case "/a/manifest.sig":
			fmt.Fprint(w, "abcd")
		case "/a/pkg":
			fmt.Fprint(w, "PKGBYTES")
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	s := &Server{ClientUpdates: &ClientUpdates{APIBase: gh.URL, HTTP: gh.Client()}}
	mux := http.NewServeMux()
	s.registerClientUpdates(mux)
	get := func(p string) (int, string) {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", p, nil))
		return rr.Code, rr.Body.String()
	}
	if c, b := get("/api/v1/clients/ps3-patcher/latest"); c != 200 || b != "version=1.2.3\n" {
		t.Fatalf("latest: %d %q", c, b)
	}
	if c, b := get("/api/v1/clients/ps3-patcher/latest.sig"); c != 200 || b != "abcd" {
		t.Fatalf("sig: %d %q", c, b)
	}
	if c, b := get("/api/v1/clients/ps3-patcher/download/OpenPak-PS3-Patcher-v1.2.3.pkg"); c != 200 || b != "PKGBYTES" {
		t.Fatalf("pkg: %d %q", c, b)
	}
	if hits != 1 {
		t.Fatalf("release looked up %d times, want 1 (cached)", hits)
	}
	for _, p := range []string{"/api/v1/clients/nope/latest", "/api/v1/clients/ps3-patcher/download/evil.pkg", "/api/v1/clients/ps3-patcher/download/..%2Fx.pkg"} {
		if c, _ := get(p); c != 404 {
			t.Fatalf("%s: %d, want 404", p, c)
		}
	}
	_ = time.Second
}

func TestClientUpdateUnconfigured(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerClientUpdates(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/clients/ps3-patcher/latest", nil))
	if rr.Code != 503 {
		t.Fatalf("code %d", rr.Code)
	}
}
