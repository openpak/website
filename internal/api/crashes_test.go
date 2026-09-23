package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/ratelimit"
	"github.com/openpak/website/internal/store"
)

func crashMultipart(t *testing.T, s *Server, bearer string, meta any, file []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if file != nil {
		fw, _ := mw.CreateFormFile("attachment", "crash.dmp")
		_, _ = fw.Write(file)
	}
	if meta != nil {
		b, _ := json.Marshal(meta)
		_ = mw.WriteField("meta", string(b))
	}
	_ = mw.Close()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/crash-reports", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCrashReports(t *testing.T) {
	s, _, db := newTestAPI(t)
	s.Crashes, _ = attachments.New(t.TempDir())
	ctx := context.Background()
	meta := map[string]any{"source": "ryujinx", "version": "1.3.2", "os": "linux", "title_id": "0100a8e016236000",
		"error_code": "2162-0002", "message": "panic at 0xdeadbeef", "fields": map[string]any{"gpu": "RADV", "top_frame": "Ryujinx.Graphics.Vulkan.Foo"}}

	// Anonymous, the file before the meta: both kept.
	rec := crashMultipart(t, s, "", meta, []byte("MDMP..."))
	var out struct {
		ID         int64
		Signature  string
		Attachment bool
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusCreated || out.ID == 0 || len(out.Signature) != 16 || !out.Attachment {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	c, _ := db.GetCrash(ctx, out.ID)
	if c.AccountID != "" || c.Attachment.Name != "crash.dmp" || c.Attachment.Size != 7 || c.TitleID != "0100A8E016236000" {
		t.Fatalf("stored: %+v", c)
	}

	// With a bearer the report is linked to the account; with a dead one it is anonymous.
	tok := mintFor(t, s, "a@x.test", "password-A-1")
	rec = crashMultipart(t, s, tok, meta, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if c, _ := db.GetCrash(ctx, out.ID); c.AccountID != "acct-a@x.test" || c.Signature != out.Signature {
		t.Fatalf("linked: %+v", c)
	}
	rec = crashMultipart(t, s, "opk_not-a-token", meta, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if c, _ := db.GetCrash(ctx, out.ID); rec.Code != http.StatusCreated || c.AccountID != "" {
		t.Fatalf("dead bearer: %d %+v", rec.Code, c)
	}

	// A bare JSON body is the meta.
	if rec := do(t, s, http.MethodPost, "/api/v1/crash-reports", "", map[string]string{"source": "app", "version": "0.8.0"}); rec.Code != http.StatusCreated {
		t.Fatalf("json: %d %s", rec.Code, rec.Body.String())
	}
	// Refused: no meta, a bad source, console via the public door, fields not an object, a file too large.
	if rec := crashMultipart(t, s, "", nil, []byte("x")); rec.Code != http.StatusBadRequest {
		t.Fatalf("no meta: %d", rec.Code)
	}
	for _, bad := range []map[string]any{{"source": "yuzu"}, {"source": "console"}, {"source": "eden", "fields": []int{1}}} {
		if rec := crashMultipart(t, s, "", bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%v: %d", bad, rec.Code)
		}
	}
	if rec := crashMultipart(t, s, "", meta, bytes.Repeat([]byte("x"), MaxCrashAttachment+1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d", rec.Code)
	}
	// Rate limited per client.
	s.CrashLimit = ratelimit.New(1, time.Minute)
	crashMultipart(t, s, "", meta, nil)
	if rec := crashMultipart(t, s, "", meta, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", rec.Code)
	}
	groups, _ := db.CrashGroups(ctx, store.CrashFilter{Source: "ryujinx"}, 10)
	if len(groups) != 1 || groups[0].Count != 4 {
		t.Fatalf("groups: %+v", groups)
	}
}

func TestInternalCrashReports(t *testing.T) {
	s, _, db := newTestAPI(t)
	s.Crashes, _ = attachments.New(t.TempDir())
	body := map[string]any{"source": "console", "received_at": "2026-09-23T10:00:00Z", "device_id": "dev-1", "account_id": "acct-1",
		"title_id": "0100152000022000", "error_code": "2110-3127", "firmware": "22.5.0", "fields": map[string]any{"result": "0x1234"},
		"attachment_b64": base64.StdEncoding.EncodeToString([]byte("erpt"))}
	if rec := doRaw(t, s, http.MethodPost, "/api/v1/internal/crash-reports", "Bearer k", body); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no key: %d", rec.Code)
	}
	t.Setenv("WEBSITE_INTERNAL_KEY", "k")
	if rec := doRaw(t, s, http.MethodPost, "/api/v1/internal/crash-reports", "", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials: %d", rec.Code)
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/crash-reports", bytes.NewReader(b))
	req.Header.Set("X-Internal-Key", "k")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out struct{ ID int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusCreated {
		t.Fatalf("internal: %d %s", rec.Code, rec.Body.String())
	}
	c, _ := db.GetCrash(context.Background(), out.ID)
	if c.Source != "console" || c.DeviceID != "dev-1" || c.Firmware != "22.5.0" || c.AccountID != "acct-1" ||
		c.ReceivedAt.UTC().Format(time.RFC3339) != "2026-09-23T10:00:00Z" || c.Attachment.Size != 4 || !strings.Contains(string(c.Fields), "0x1234") {
		t.Fatalf("stored: %+v", c)
	}
	body["source"] = "ryujinx"
	if rec := doRaw(t, s, http.MethodPost, "/api/v1/internal/crash-reports", "Bearer k", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-console source: %d", rec.Code)
	}
	body["source"], body["received_at"] = "console", 1790000000
	if rec := doRaw(t, s, http.MethodPost, "/api/v1/internal/crash-reports", "Bearer k", body); rec.Code != http.StatusCreated {
		t.Fatalf("unix received_at: %d", rec.Code)
	}
	body["received_at"] = "yesterday"
	if rec := doRaw(t, s, http.MethodPost, "/api/v1/internal/crash-reports", "Bearer k", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad received_at: %d", rec.Code)
	}
}

func TestInternalCrashBadBase64(t *testing.T) {
	s, _, _ := newTestAPI(t)
	s.Crashes, _ = attachments.New(t.TempDir())
	t.Setenv("WEBSITE_INTERNAL_KEY", "k")
	rec := doRaw(t, s, http.MethodPost, "/api/v1/internal/crash-reports", "Bearer k", map[string]any{"source": "console", "attachment_b64": "!!!not base64"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad base64: %d %s", rec.Code, rec.Body.String())
	}
}
