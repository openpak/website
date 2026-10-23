// openpak-opsd is the host agent behind the admin dashboard's services page.
// The website runs in a container and must never hold the podman socket: a
// web process that can talk to podman can start anything on the host. So the
// one thing that may touch podman and systemd --user runs here, on the host,
// as the user who owns the rootless containers, and offers the website a
// handful of verbs over an authenticated HTTP API: list, restart, stop,
// start, update. Nothing else — no exec, no logs, no arbitrary units.
//
// See docs/opsd.md for the environment, the unit to run it under and the
// security notes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// defaultAddr is the website block's first spare port (20010 +3, see
// the port plan (https://docs.openpak.org/contributors/ports/)): opsd only exists for the website, so it lives in its block
// rather than taking a service block of its own. Loopback by default — the
// operator widens it on purpose.
const defaultAddr = "127.0.0.1:20013"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("openpak-opsd", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	a, err := newAgentFromEnv(log)
	if err != nil {
		return err
	}
	if a.key == "" {
		// Not fatal: the process stays up and answers 503, so the dashboard
		// says "not configured" instead of "unreachable".
		log.Warn("OPSD_KEY is not set; every API call will be refused")
	}
	every := 15 * time.Minute
	if v := os.Getenv("OPSD_UPDATE_CHECK"); v != "" {
		if every, err = time.ParseDuration(v); err != nil || every <= 0 {
			return fmt.Errorf("OPSD_UPDATE_CHECK %q: want a positive duration like 15m", v)
		}
	}
	addr := envOr("OPSD_ADDR", defaultAddr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.updateLoop(ctx, every)

	srv := &http.Server{
		Addr:              addr,
		Handler:           a.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: an update is a pull plus a restart and can take
		// minutes; every exec underneath carries its own deadline instead.
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("openpak-opsd listening", "addr", addr, "host", a.host, "prefix", a.prefix)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// execRun is the real runner. Only stdout is returned because podman prints
// warnings on stderr that would corrupt the JSON; stderr goes into the error
// instead, where it is the useful part.
func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, lastLine(ee.Stderr))
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// lastLine keeps an error message to what a person can read in a toast:
// podman and systemctl put the reason on the final line.
func lastLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
