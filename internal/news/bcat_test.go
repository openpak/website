package news

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBCATProxyPreservesDeliveryAndConditionalRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/emulator/v1/bcat/titles/0100f8f0000a2000/files/vsdata/VSSetting_0.byaml" || r.URL.RawQuery != "revision=1" {
			t.Errorf("wrong upstream URL: %s", r.URL)
		}
		for _, name := range []string{"Authorization", "Cookie", "X-Internal-Key", "X-Operator-Key"} {
			if r.Header.Get(name) != "" {
				t.Errorf("forwarded %s", name)
			}
		}
		w.Header().Set("ETag", `"revision-1"`)
		w.Header().Set("X-Content-SHA256", "test-hash")
		if r.Header.Get("If-None-Match") == `"revision-1"` {
			w.WriteHeader(304)
			return
		}
		w.Write([]byte{0, 1, 2, 255})
	}))
	defer upstream.Close()
	proxy, err := NewBCATProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, conditional := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/emulator/v1/bcat/titles/0100f8f0000a2000/files/vsdata/VSSetting_0.byaml?revision=1", nil)
		for _, name := range []string{"Authorization", "Cookie", "X-Internal-Key", "X-Operator-Key"} {
			r.Header.Set(name, "private")
		}
		if conditional {
			r.Header.Set("If-None-Match", `"revision-1"`)
		}
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Header().Get("ETag") != `"revision-1"` || w.Header().Get("X-Content-SHA256") != "test-hash" {
			t.Fatalf("missing delivery headers: %v", w.Header())
		}
		if conditional {
			if w.Code != 304 || w.Body.Len() != 0 {
				t.Fatalf("conditional: %d %q", w.Code, w.Body.Bytes())
			}
		} else if w.Code != 200 || w.Body.String() != string([]byte{0, 1, 2, 255}) {
			t.Fatalf("payload: %d %q", w.Code, w.Body.Bytes())
		}
	}
}

func TestBCATProxyConfiguration(t *testing.T) {
	if proxy, err := NewBCATProxy(""); err != nil || proxy != nil {
		t.Fatalf("empty configuration: %v %v", proxy, err)
	}
	for _, value := range []string{"file:///tmp", "http:///missing", "http://user:secret@host", "http://host/api", "http://host?query=1", "http://host#fragment"} {
		if _, err := NewBCATProxy(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
