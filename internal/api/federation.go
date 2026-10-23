package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/clientip"
	"github.com/openpak/website/internal/core"
)

// bearerKey is the node or server key a federation call carries.
func bearerKey(r *http.Request, prefix string) (string, bool) {
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return key, ok && strings.HasPrefix(key, prefix)
}

// fedError answers a failed relay to the core in words an operator can act on.
func (s *Server) fedError(w http.ResponseWriter, what string, err error) {
	switch {
	case strings.Contains(err.Error(), "credentials rejected"):
		writeError(w, http.StatusUnauthorized, "unknown key")
	case errors.Is(err, core.ErrTooSoon):
		writeError(w, http.StatusTooManyRequests, strings.TrimPrefix(err.Error(), core.ErrTooSoon.Error()+": "))
	case errors.Is(err, core.ErrBlocked):
		writeError(w, http.StatusForbidden, strings.TrimPrefix(err.Error(), core.ErrBlocked.Error()+": "))
	case errors.Is(err, core.ErrConflict):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), core.ErrConflict.Error()+": "))
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "unknown server")
	default:
		s.log.Error(what, "err", err)
		writeError(w, http.StatusBadGateway, what+" failed")
	}
}

// handleJoin enrols one server under a node, with the node key as the bearer,
// and returns the server's own key, once. Joining a name again replaces that
// server's key (a reinstalled box), so the node key is all an operator keeps.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if s.Federation == nil {
		writeError(w, http.StatusServiceUnavailable, "federation not configured")
		return
	}
	key, ok := bearerKey(r, "opnode_")
	if !ok {
		writeError(w, http.StatusUnauthorized, "node key required")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id, serverKey, err := s.Federation.JoinServerAs(r.Context(), key, in.Name)
	if err != nil {
		s.fedError(w, "join", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_id": id, "key": serverKey})
}

// handleAnnounce is a server's minute-by-minute announce: its roles and its
// hardware report, with its own key as the bearer. The address the request
// came from is attached here, so the core can compare it with the IPv4 the
// server claims.
func (s *Server) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	if s.Federation == nil {
		writeError(w, http.StatusServiceUnavailable, "federation not configured")
		return
	}
	key, ok := bearerKey(r, "opsrv_")
	if !ok {
		if _, isNode := bearerKey(r, "opnode_"); isNode {
			writeError(w, http.StatusUnauthorized, "announce with a server key: join the server first (POST /api/v1/federation/join)")
			return
		}
		writeError(w, http.StatusUnauthorized, "server key required")
		return
	}
	var in struct {
		Services map[string]string `json:"services"`
		Report   core.ServerReport `json:"report"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	res, err := s.Federation.AnnounceAs(r.Context(), key, in.Services, in.Report, clientip.Of(r))
	if err != nil {
		s.fedError(w, "announce", err)
		return
	}
	if res.Problems == nil {
		res.Problems = []string{}
	}
	writeJSON(w, http.StatusOK, res)
}

// The bandwidth self-test. A server asks for one transfer per direction per
// day (the core holds the rate limit); home times it and writes the result
// itself, so a server can make its figure worse but never better. The RTT is
// the server's own measurement and is labelled as such.

// handleSelfTestDown streams the reserved number of bytes to the server and
// records how fast they left.
func (s *Server) handleSelfTestDown(w http.ResponseWriter, r *http.Request) {
	if s.Federation == nil {
		writeError(w, http.StatusServiceUnavailable, "federation not configured")
		return
	}
	key, ok := bearerKey(r, "opsrv_")
	if !ok {
		writeError(w, http.StatusUnauthorized, "server key required")
		return
	}
	id, n, err := s.Federation.StartSelfTestAs(r.Context(), key, "down")
	if err != nil {
		s.fedError(w, "self-test", err)
		return
	}
	// A transfer that cannot finish in a minute is not worth holding a
	// connection for; it records nothing, which fails the server.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(selfTestDeadline))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	buf := make([]byte, 64<<10) // zeros: the test measures the path, and nothing compresses in between that would not also compress game traffic
	flusher, _ := w.(http.Flusher)
	start := time.Now()
	var sent int64
	for sent < n {
		chunk := buf
		if rest := n - sent; rest < int64(len(chunk)) {
			chunk = chunk[:rest]
		}
		k, err := w.Write(chunk)
		sent += int64(k)
		if err != nil {
			return // the server hung up: no result, and the day's slot is spent
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
	s.recordSelfTest(r, id, "down", sent, time.Since(start), 0)
}

// handleSelfTestUp reads the reserved number of bytes from the server and
// records how fast they arrived. X-OpenPak-RTT-Ms carries the server's own
// round-trip measurement to home.
func (s *Server) handleSelfTestUp(w http.ResponseWriter, r *http.Request) {
	if s.Federation == nil {
		writeError(w, http.StatusServiceUnavailable, "federation not configured")
		return
	}
	key, ok := bearerKey(r, "opsrv_")
	if !ok {
		writeError(w, http.StatusUnauthorized, "server key required")
		return
	}
	id, n, err := s.Federation.StartSelfTestAs(r.Context(), key, "up")
	if err != nil {
		s.fedError(w, "self-test", err)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(selfTestDeadline))
	body := http.MaxBytesReader(w, r.Body, n)
	first := make([]byte, 1)
	if _, err := io.ReadFull(body, first); err != nil {
		writeError(w, http.StatusBadRequest, "empty upload")
		return
	}
	start := time.Now() // from the first byte: connection setup is not bandwidth
	got, _ := io.Copy(io.Discard, body)
	elapsed := time.Since(start)
	got++
	if got < n {
		writeError(w, http.StatusBadRequest, "upload shorter than the "+strconv.FormatInt(n, 10)+" bytes reserved")
		return
	}
	rtt, _ := strconv.Atoi(r.Header.Get("X-OpenPak-RTT-Ms"))
	if rtt < 0 || rtt > 60000 {
		rtt = 0
	}
	mbps := s.recordSelfTest(r, id, "up", got, elapsed, rtt)
	writeJSON(w, http.StatusOK, map[string]any{"server_id": id, "up_mbps": mbps})
}

// selfTestDeadline bounds one transfer. With the default 100 MB it fails
// anything slower than about 13 Mbps, far under the measured floor.
const selfTestDeadline = time.Minute

func (s *Server) recordSelfTest(r *http.Request, id, direction string, bytes int64, elapsed time.Duration, rtt int) float64 {
	if elapsed < time.Millisecond {
		elapsed = time.Millisecond
	}
	mbps := float64(bytes) * 8 / elapsed.Seconds() / 1e6
	if err := s.Federation.RecordSelfTest(r.Context(), id, direction, mbps, rtt); err != nil {
		s.log.Error("record self-test", "server", id, "err", err)
	}
	return mbps
}
