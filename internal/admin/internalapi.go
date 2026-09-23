package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// keyClient calls a service's internal HTTP API with X-Internal-Key: the
// news admin API and nx-baas's store admin API share the scheme.
type keyClient struct {
	URL, Key string
}

func newKeyClient(baseURL, key string) *keyClient {
	if baseURL == "" || key == "" {
		return nil
	}
	return &keyClient{URL: strings.TrimRight(baseURL, "/"), Key: key}
}

// apiError is a non-2xx answer, with the service's own words. The news API
// sends {"error": text}; the store API {"error": code, "message": text}.
type apiError struct {
	Status        int
	Code, Message string
}

func (e *apiError) Error() string {
	switch {
	case e.Message != "" && e.Code != "":
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	case e.Message != "":
		return e.Message
	case e.Code != "":
		return e.Code
	}
	return http.StatusText(e.Status)
}

// send makes one call. body may be nil; a non-nil out is decoded from a 2xx.
func (c *keyClient) send(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Key", c.Key)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&e)
		if e.Message == "" {
			return &apiError{Status: resp.StatusCode, Message: e.Error}
		}
		return &apiError{Status: resp.StatusCode, Code: e.Error, Message: e.Message}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

// sendJSON marshals in (nil sends no body) and decodes out.
func (c *keyClient) sendJSON(ctx context.Context, method, path string, in, out any) error {
	if in == nil {
		return c.send(ctx, method, path, "", nil, out)
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.send(ctx, method, path, "application/json", bytes.NewReader(b), out)
}

// proxy streams a raw response (an uploaded image) back to the browser.
func (c *keyClient) proxy(w http.ResponseWriter, r *http.Request, path string) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.URL+path, nil)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "the service did not answer", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.NotFound(w, r)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "image/png" && ct != "image/jpeg" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, resp.Body)
}
