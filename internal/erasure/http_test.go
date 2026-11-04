package erasure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseServices(t *testing.T) {
	got, err := ParseServices("nex=http://a:1/, nex=http://b:2\nxbl=http://c:3;")
	if err != nil || len(got) != 3 || got[0].URL != "http://a:1" {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ParseServices("oops"); err == nil {
		t.Fatal("want error")
	}
}

func TestHTTPStepCallsEveryAddressAndSendsKey(t *testing.T) {
	var hits int
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != "DELETE" || r.URL.Path != "/internal/accounts/abc/data" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(400)
		}
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	st := HTTPStep("x", []string{bad.URL, ok.URL}, "k", http.DefaultClient)
	if err := st.Erase(context.Background(), "abc"); err == nil {
		t.Fatal("want failure from the bad address")
	}
	if hits != 1 {
		t.Fatalf("good address must still be called, hits=%d", hits)
	}
	if err := HTTPStep("x", []string{ok.URL}, "", http.DefaultClient).Erase(context.Background(), "abc"); err == nil {
		t.Fatal("no key must fail")
	}
}

func TestReportFlagsMissing(t *testing.T) {
	f := &Finaliser{Expected: []string{"a", "b"}, Steps: []Step{{Name: "a", Erase: func(context.Context, string) error { return nil }}}}
	f.record("a", nil)
	r := f.Report()
	if len(r) != 2 || !r[0].Configured || !r[0].OK || r[1].Configured {
		t.Fatalf("%+v", r)
	}
}
