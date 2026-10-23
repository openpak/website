package turnstile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNilIsOff(t *testing.T) {
	var v *Verifier = New("", "secret")
	if v != nil || v.Verify(context.Background(), "", "") != nil || v.Key() != "" {
		t.Fatal("an unconfigured verifier must pass everything and draw no widget")
	}
}

func TestVerify(t *testing.T) {
	var gotIP, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotIP, gotSecret = r.PostFormValue("remoteip"), r.PostFormValue("secret")
		if r.PostFormValue("response") == "good" {
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	defer srv.Close()
	v := New("site", "sec")
	v.URL = srv.URL
	ctx := context.Background()
	if err := v.Verify(ctx, "good", "198.51.100.1"); err != nil {
		t.Fatalf("good token: %v", err)
	}
	if gotIP != "198.51.100.1" || gotSecret != "sec" {
		t.Errorf("sent remoteip=%q secret=%q", gotIP, gotSecret)
	}
	if err := v.Verify(ctx, "bad", ""); !errors.Is(err, ErrFailed) {
		t.Errorf("bad token: %v", err)
	}
	if err := v.Verify(ctx, "", ""); !errors.Is(err, ErrFailed) {
		t.Errorf("missing token: %v", err)
	}
	// Fail closed: Cloudflare unreachable is a refusal, not a pass.
	srv.Close()
	if err := v.Verify(ctx, "good", ""); err == nil {
		t.Error("an unreachable siteverify must not pass")
	}
}
