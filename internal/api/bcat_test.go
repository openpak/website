package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBCATRoutesReportMissingDeliveryAndRelayUnknownTitle(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerBCAT(mux)
	for _, path := range []string{
		"/api/emulator/v1/bcat/titles",
		"/api/emulator/v1/bcat/titles/0100f8f0000a2000",
		"/api/emulator/v1/bcat/titles/0100f8f0000a2000/files/vsdata/VSSetting_0.byaml",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 503 {
			t.Fatalf("unconfigured %s: %d", path, w.Code)
		}
	}
	s.BCAT = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/emulator/v1/bcat/titles/0100f8f0000a2000", nil))
	if w.Code != 404 {
		t.Fatalf("upstream unknown title: %d", w.Code)
	}
}
