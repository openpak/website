package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/logstore"
)

// Service logs (/admin/logs): pick a service and a UTC day (90 are kept),
// filter by level and text, page back, tail the newest lines, download the day.
// The lines come from the host collector (cmd/openpak-logd, docs/logs.md).

const logPage = 300

// LogSource is the log database (*logstore.Store).
type LogSource interface {
	Lines(ctx context.Context, q logstore.Query) ([]logstore.Line, error)
	Services(ctx context.Context, day time.Time) ([]logstore.ServiceCount, error)
	Download(ctx context.Context, service string, day time.Time, fn func(string) error) error
}

type logsView struct {
	base
	Services   []logstore.ServiceCount
	Days       []string
	Service    string
	Day        string
	Level      int
	Text       string
	Lines      []logstore.Line
	NextBefore int64
	NewestID   int64
	Error      string
	Levels     []string
	Query      url.Values
}

// logQuery reads the filter form. A missing day is today, a missing level is
// every level.
func logQuery(q url.Values, now time.Time) (logstore.Query, string) {
	d := now.UTC().Truncate(24 * time.Hour)
	if t, err := time.Parse("2006-01-02", q.Get("day")); err == nil && !t.After(d) && !t.Before(d.AddDate(0, 0, -logstore.Keep)) {
		d = t
	}
	lv, _ := strconv.Atoi(q.Get("level"))
	lq := logstore.Query{Service: strings.TrimSpace(q.Get("service")), Day: d, MinLevel: min(max(lv, 0), logstore.Error),
		Text: strings.TrimSpace(q.Get("q")), Limit: logPage}
	lq.BeforeID, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	lq.AfterID, _ = strconv.ParseInt(q.Get("after"), 10, 64)
	return lq, d.Format("2006-01-02")
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	v := logsView{base: s.page(r, ws, i18n.T("en", "admin.logs")), Levels: logstore.LevelNames[:]}
	if s.Logs == nil {
		v.Error = i18n.T("en", "admin.logs.off")
		s.render(w, http.StatusOK, "admin_logs.html", v)
		return
	}
	now := time.Now()
	lq, day := logQuery(r.URL.Query(), now)
	v.Day, v.Service, v.Level, v.Text = day, lq.Service, lq.MinLevel, lq.Text
	for i := range logstore.Keep + 1 {
		v.Days = append(v.Days, now.UTC().AddDate(0, 0, -i).Format("2006-01-02"))
	}
	ctx := r.Context()
	svcs, err := s.Logs.Services(ctx, lq.Day)
	if err != nil {
		s.log.Error("log services", "err", err)
		v.Error = i18n.T("en", "admin.logs.err")
	}
	v.Services = svcs
	if lq.Service != "" && v.Error == "" {
		lq.AfterID = 0
		lines, err := s.Logs.Lines(ctx, lq)
		if err != nil {
			s.log.Error("log lines", "err", err)
			v.Error = i18n.T("en", "admin.logs.err")
		}
		v.Lines = lines
		if len(lines) > 0 {
			v.NewestID = lines[0].ID
			if len(lines) == logPage {
				v.NextBefore = lines[len(lines)-1].ID
			}
		}
	}
	v.Query = url.Values{}
	for _, k := range []string{"service", "day", "level", "q"} {
		if x := r.URL.Query().Get(k); x != "" {
			v.Query.Set(k, x)
		}
	}
	s.render(w, http.StatusOK, "admin_logs.html", v)
}

// handleLogsData answers the tail: lines newer than ?after, as JSON.
func (s *Server) handleLogsData(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r); !ok {
		return
	}
	if s.Logs == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	lq, _ := logQuery(r.URL.Query(), time.Now())
	lq.BeforeID = 0
	if lq.Service == "" || lq.AfterID <= 0 {
		http.Error(w, "service and after are required", http.StatusBadRequest)
		return
	}
	lines, err := s.Logs.Lines(r.Context(), lq)
	if err != nil {
		s.log.Error("log tail", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	type out struct {
		ID    int64  `json:"id"`
		Time  string `json:"time"`
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}
	res := make([]out, 0, len(lines))
	for _, l := range lines {
		res = append(res, out{l.ID, l.TS.UTC().Format("15:04:05.000"), l.LevelName(), l.Msg})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}

// handleLogsDownload sends one service-day as a text file, oldest first.
func (s *Server) handleLogsDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r); !ok {
		return
	}
	if s.Logs == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	lq, day := logQuery(r.URL.Query(), time.Now())
	if lq.Service == "" {
		http.Error(w, "service is required", http.StatusBadRequest)
		return
	}
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, lq.Service)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-`+day+`.log"`)
	err := s.Logs.Download(r.Context(), lq.Service, lq.Day, func(line string) error {
		_, err := w.Write([]byte(line + "\n"))
		return err
	})
	if err != nil {
		s.log.Error("log download", "err", err)
	}
}
