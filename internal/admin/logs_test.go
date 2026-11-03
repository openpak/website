package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openpak/website/internal/logstore"
	"github.com/openpak/website/internal/store"
)

type fakeLogs struct{ lines []logstore.Line }

func (f *fakeLogs) Lines(_ context.Context, q logstore.Query) ([]logstore.Line, error) {
	var out []logstore.Line
	for i := len(f.lines) - 1; i >= 0; i-- {
		l := f.lines[i]
		if l.Service == q.Service && l.Level >= q.MinLevel && strings.Contains(strings.ToLower(l.Msg), strings.ToLower(q.Text)) &&
			(q.AfterID == 0 || l.ID > q.AfterID) {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeLogs) Services(context.Context, time.Time) ([]logstore.ServiceCount, error) {
	return []logstore.ServiceCount{{Service: "openpak-account", Lines: 3, Errors: 1}, {Service: "openpak-nx-baas", Lines: 40}}, nil
}

func (f *fakeLogs) Download(_ context.Context, svc string, _ time.Time, fn func(string) error) error {
	for _, l := range f.lines {
		if l.Service == svc {
			if err := fn(logstore.FormatLine(l)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sampleLogs() *fakeLogs {
	t0 := time.Now().UTC().Truncate(time.Hour)
	mk := func(i int64, lv int, m string) logstore.Line {
		return logstore.Line{ID: i, TS: t0.Add(time.Duration(i) * time.Second), Service: "openpak-account", Level: lv, Msg: m}
	}
	return &fakeLogs{lines: []logstore.Line{
		mk(1, logstore.Info, "account core listening: grpc=:20001 http=:20000"),
		mk(2, logstore.Info, "http GET /v1/api/people/abc -> 200 4ms"),
		mk(3, logstore.Warn, "http POST /v1/api/oauth20/access_token/generate -> 401 2ms"),
		mk(4, logstore.Error, "ERROR core grpc: dial tcp 10.89.2.61:20001: connect: connection refused"),
		mk(5, logstore.Info, "http GET /healthz -> 200 <html>&\"x\"</html>"),
	}}
}

func TestLogsViewerGateAndFilter(t *testing.T) {
	s, db, _ := newAdmin(t)
	s.Logs = sampleLogs()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-normal"})
	for _, p := range []string{"/admin/logs", "/admin/logs/data?service=x&after=1", "/admin/logs/download?service=openpak-account"} {
		if c := get(p).Code; c != http.StatusForbidden {
			t.Fatalf("%s: non-admin got %d", p, c)
		}
	}
	_ = db.PutWebSession(context.Background(), store.WebSession{CoreSessionID: "sess-admin", AccountID: "acct-admin"})
	rec := get("/admin/logs?service=openpak-account&level=2&q=")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "connection refused") || !strings.Contains(body, "401") || strings.Contains(body, "listening") {
		t.Fatalf("level filter: %d %s", rec.Code, body)
	}
	if !strings.Contains(get("/admin/logs?service=openpak-account&q=healthz").Body.String(), "&lt;html&gt;") {
		t.Errorf("message not escaped")
	}
	if d := get("/admin/logs/download?service=openpak-account"); d.Code != 200 || !strings.Contains(d.Body.String(), "ERROR ERROR core") ||
		!strings.Contains(d.Header().Get("Content-Disposition"), "openpak-account-") {
		t.Errorf("download: %d %q %q", d.Code, d.Body.String(), d.Header())
	}
	if d := get("/admin/logs/data?service=openpak-account&after=3"); !strings.Contains(d.Body.String(), `"id":5`) || strings.Contains(d.Body.String(), `"id":2`) {
		t.Errorf("tail: %s", d.Body.String())
	}
	s.Logs = nil
	if !strings.Contains(get("/admin/logs").Body.String(), "not configured") {
		t.Errorf("unconfigured page")
	}
}
