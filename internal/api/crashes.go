package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/session"
	"github.com/openpak/website/internal/store"
)

// Crash reports. Public ingest for the emulators, the homebrew and the app;
// an internal one for nx-baas, which relays the error reports consoles send.
// Stored in crash_reports, files under WEBSITE_ATTACHMENTS_DIR, triaged on
// /admin/crashes.

// MaxCrashAttachment is the largest file a crash report may carry.
const MaxCrashAttachment = 20 << 20

// crashMeta is the `meta` part of a public report.
type crashMeta struct {
	Source    string          `json:"source"`
	Version   string          `json:"version"`
	OS        string          `json:"os"`
	TitleID   string          `json:"title_id"`
	ErrorCode string          `json:"error_code"`
	Message   string          `json:"message"`
	Fields    json.RawMessage `json:"fields"`
}

func (s *Server) registerCrashes(mux *http.ServeMux) {
	h := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("POST /api/v1"+pattern, h) }
	h("/crash-reports", s.handleCrashReport)
	h("/internal/crash-reports", s.handleInternalCrashReport)
}

// crashAccount links a report to the account behind a bearer, if one was
// sent and is still good. A missing, stale or revoked bearer (a banned
// account's included) files the report anonymously rather than refusing it.
func (s *Server) crashAccount(r *http.Request) string {
	raw := session.BearerFromRequest(r)
	if raw == "" {
		return ""
	}
	tok, err := s.DB.APITokenByHash(r.Context(), s.Sessions.HashToken(raw))
	if err != nil {
		return ""
	}
	if _, err := s.Core.ListSessions(r.Context(), tok.CoreSessionID); err != nil {
		if errors.Is(err, core.ErrSessionInvalid) {
			_ = s.DB.RevokeAPIToken(r.Context(), tok.ID)
		}
		return ""
	}
	return tok.AccountID
}

func (s *Server) crashFailed(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "invalid request: "))
	case errors.Is(err, attachments.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "the attachment is larger than 20 MB")
	default:
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "the attachment is larger than 20 MB")
			return
		}
		s.log.Error("crash report", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// handleCrashReport: multipart with `meta` (JSON) and an optional
// `attachment`, streamed to disk; or a bare JSON body that is the meta.
func (s *Server) handleCrashReport(w http.ResponseWriter, r *http.Request) {
	if !s.CrashLimit.Allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxCrashAttachment+1<<20)
	var meta crashMeta
	var att store.Attachment
	var gotMeta bool
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&meta); err != nil {
			writeError(w, http.StatusBadRequest, "meta is not valid JSON")
			return
		}
		gotMeta = true
	case "multipart/form-data":
		mr, err := r.MultipartReader()
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				s.dropCrashFile(att)
				s.crashFailed(w, err)
				return
			}
			switch part.FormName() {
			case "meta":
				if err := json.NewDecoder(io.LimitReader(part, 256<<10)).Decode(&meta); err != nil {
					s.dropCrashFile(att)
					writeError(w, http.StatusBadRequest, "meta is not valid JSON")
					return
				}
				gotMeta = true
			case "attachment":
				if att.Key != "" || s.Crashes == nil {
					// One file per report; with no storage the report is still kept.
					_, _ = io.Copy(io.Discard, part)
					continue
				}
				saved, err := s.Crashes.Save("crashes", part, MaxCrashAttachment)
				if err != nil {
					s.crashFailed(w, err)
					return
				}
				att = store.Attachment{Key: saved.Key, Name: attachments.CleanName(part.FileName()), ContentType: saved.ContentType, Size: saved.Size}
			default:
				_, _ = io.Copy(io.Discard, part)
			}
		}
	default:
		writeError(w, http.StatusBadRequest, "send multipart/form-data with a meta part, or JSON")
		return
	}
	if !gotMeta {
		s.dropCrashFile(att)
		writeError(w, http.StatusBadRequest, "meta is required")
		return
	}
	if strings.EqualFold(strings.TrimSpace(meta.Source), "console") {
		s.dropCrashFile(att)
		writeError(w, http.StatusBadRequest, "source must be one of ryujinx, citron, eden, nro, app")
		return
	}
	c, err := s.DB.CreateCrash(r.Context(), store.CrashReport{Source: meta.Source, Version: meta.Version, OS: meta.OS,
		TitleID: meta.TitleID, ErrorCode: meta.ErrorCode, Message: meta.Message, Fields: meta.Fields,
		AccountID: s.crashAccount(r), Attachment: att})
	if err != nil {
		s.dropCrashFile(att)
		s.crashFailed(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": c.ID, "signature": c.Signature, "attachment": att.Key != ""})
}

func (s *Server) dropCrashFile(a store.Attachment) {
	if a.Key != "" && s.Crashes != nil {
		s.Crashes.Remove(a.Key)
	}
}

// handleInternalCrashReport is nx-baas relaying a console's error report.
func (s *Server) handleInternalCrashReport(w http.ResponseWriter, r *http.Request) {
	if !internalAuthorized(w, r) {
		return
	}
	// A base64 file is a third larger than the file; leave room for the rest.
	r.Body = http.MaxBytesReader(w, r.Body, MaxCrashAttachment*4/3+2<<20)
	var req struct {
		Source        string          `json:"source"`
		ReceivedAt    json.RawMessage `json:"received_at"`
		DeviceID      string          `json:"device_id"`
		AccountID     string          `json:"account_id"`
		TitleID       string          `json:"title_id"`
		ErrorCode     string          `json:"error_code"`
		Firmware      string          `json:"firmware"`
		Message       string          `json:"message"`
		Fields        json.RawMessage `json:"fields"`
		AttachmentB64 string          `json:"attachment_b64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "the attachment is larger than 20 MB")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Source != "console" {
		writeError(w, http.StatusBadRequest, `source must be "console"`)
		return
	}
	received, ok := parseReceivedAt(req.ReceivedAt)
	if !ok {
		writeError(w, http.StatusBadRequest, "received_at must be RFC 3339 or unix seconds")
		return
	}
	var att store.Attachment
	if req.AttachmentB64 != "" && s.Crashes != nil {
		saved, err := s.Crashes.Save("crashes", base64.NewDecoder(base64.StdEncoding, strings.NewReader(req.AttachmentB64)), MaxCrashAttachment)
		if err != nil {
			// Only bad input is a 4xx: nx-baas drops a report it was refused
			// with one, and retries anything else.
			var corrupt base64.CorruptInputError
			if errors.As(err, &corrupt) {
				writeError(w, http.StatusBadRequest, "attachment_b64 is not base64")
				return
			}
			s.crashFailed(w, err)
			return
		}
		att = store.Attachment{Key: saved.Key, Name: "console-report.bin", ContentType: saved.ContentType, Size: saved.Size}
	}
	c, err := s.DB.CreateCrash(r.Context(), store.CrashReport{Source: "console", TitleID: req.TitleID, ErrorCode: req.ErrorCode,
		Message: req.Message, Fields: req.Fields, AccountID: req.AccountID, DeviceID: req.DeviceID, Firmware: req.Firmware,
		ReceivedAt: received, Attachment: att})
	if err != nil {
		s.dropCrashFile(att)
		s.crashFailed(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": c.ID, "signature": c.Signature, "attachment": att.Key != ""})
}

// parseReceivedAt reads RFC 3339 or unix seconds; absent is "now".
func parseReceivedAt(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, true
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if str == "" {
			return time.Time{}, true
		}
		t, err := time.Parse(time.RFC3339, str)
		return t, err == nil
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		if sec, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
			return time.Unix(sec, 0), true
		}
	}
	return time.Time{}, false
}
