package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRegistrationPreservesHandleFailures(t *testing.T) {
	for _, want := range []error{ErrHandleTaken, ErrHandleInvalid, ErrHandleReserved, ErrHandleProfane} {
		t.Run(want.Error(), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]string
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request["handle"] != "AuroraOne" || request["display_name"] != "Aurora Player" {
					t.Errorf("handle/name mixed: %v", request)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": want.Error()})
			}))
			defer srv.Close()
			c, err := NewHTTP(srv.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			err = c.RegisterWithProfile(context.Background(), "aurora@example.org", "correct horse battery", "Aurora Player", "AuroraOne", "IE", "1990-01-01")
			if !errors.Is(err, want) {
				t.Fatalf("lost handle error: %v", err)
			}
		})
	}
}
