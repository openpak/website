package erasure

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/website/internal/store"
)

// The mods step sends the erase call with the internal key, fails (so the
// account waits) when the service errs or is not configured, and runs before
// the core finalizes.
func TestModsStep(t *testing.T) {
	ctx := context.Background()
	var got []string
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.EscapedPath()+" "+r.Header.Get("Authorization"))
		w.WriteHeader(status)
		w.Write([]byte(`{"anonymised":1,"deleted":2,"favourites":0}`))
	}))
	defer ts.Close()

	st := ModsStep(ts.URL+"/", "k3y", ts.Client())
	if st.Name != "mods" {
		t.Fatalf("name %q", st.Name)
	}
	if err := st.Erase(ctx, "acct a@x.test"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "DELETE /internal/accounts/acct%20a@x.test/mods Bearer k3y" {
		t.Fatalf("request %v", got)
	}
	status = http.StatusServiceUnavailable
	if err := st.Erase(ctx, "a1"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("503 not an error: %v", err)
	}
	if err := ModsStep("", "", nil).Erase(ctx, "a1"); !errors.Is(err, ErrModsUnavailable) {
		t.Fatalf("unconfigured: %v", err)
	}

	// In the finaliser: a failing mods step keeps the account from finalizing.
	fc := &fakeCore{due: []string{"a1"}}
	f := &Finaliser{Core: fc, Lock: store.NewMemory(), Log: quiet(), Steps: []Step{st}}
	if res, err := f.RunOnce(ctx); err != nil || res.Failed != 1 || len(fc.finalized) != 0 {
		t.Fatalf("mods down: %+v %v finalized=%v", res, err, fc.finalized)
	}
	status = http.StatusOK
	if res, err := f.RunOnce(ctx); err != nil || res.Erased != 1 || strings.Join(fc.finalized, ",") != "a1" {
		t.Fatalf("mods back: %+v %v finalized=%v", res, err, fc.finalized)
	}
}
