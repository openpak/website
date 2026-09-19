package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A code is only reachable while some handler still writes its message word
// for word; this fails when a message is reworded without its entry.
func TestErrorCodesMatchHandlers(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var src strings.Builder
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			b, _ := os.ReadFile(f)
			src.Write(b)
		}
	}
	for msg := range errorCodes {
		if strings.Count(src.String(), `"`+msg+`"`) < 2 {
			t.Errorf("no handler writes %q", msg)
		}
	}
	w := httptest.NewRecorder()
	writeError(w, http.StatusForbidden, "wrong password")
	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["code"] != "wrong_password" || body["error"] != "wrong password" {
		t.Errorf("body %v", body)
	}
	if got := errorCode(http.StatusBadRequest, "something new"); got != "bad_request" {
		t.Errorf("fallback %q", got)
	}
}
