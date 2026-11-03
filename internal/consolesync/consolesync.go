// Package consolesync makes the OpenPak password the Wii U / 3DS console password. A console
// signs in with NintendoPasswordHash(password, pid), which nn-account can only compute from the
// plain password, and the website sees that only when a person signs in, changes it or resets it.
// So it wraps the core's HTTP client: after a successful call that carried the plain password,
// it hands the password to nn-account in the background. A slow or failed nn-account never
// blocks or fails the sign-in, and the password is never logged.
package consolesync

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/openpak/website/internal/adapter"
	"github.com/openpak/website/internal/core"
)

type HTTP struct {
	core.HTTP
	NN  *adapter.Client
	Log *slog.Logger
	// Wait lets tests join the background calls.
	Wait sync.WaitGroup

	mu      sync.Mutex
	pending map[string]pendingPassword // 2FA challenge -> the password that earned it
}

type pendingPassword struct {
	password string
	expires  time.Time
}

// New wraps c. With a nil adapter nothing is synced and c is returned unchanged.
func New(c core.HTTP, nn *adapter.Client, log *slog.Logger) core.HTTP {
	if nn == nil {
		return c
	}
	return &HTTP{HTTP: c, NN: nn, Log: log, pending: map[string]pendingPassword{}}
}

// push sets the account's console credential from the OpenPak password, best effort.
func (h *HTTP) push(accountID, password string) {
	if accountID == "" || password == "" {
		return
	}
	h.Wait.Add(1)
	go func() {
		defer h.Wait.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		status, _, err := h.NN.SetConsolePassword(ctx, accountID, password, "sync")
		if err != nil {
			h.Log.Warn("console password sync failed", "account", accountID, "err", err)
			return
		}
		h.Log.Debug("console password synced", "account", accountID, "status", status)
	}()
}

// pushSession resolves the account behind a fresh core session, then pushes.
func (h *HTTP) pushSession(coreSession, password string) {
	h.Wait.Add(1)
	go func() {
		defer h.Wait.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		me, err := h.HTTP.Whoami(ctx, coreSession)
		if err != nil {
			h.Log.Warn("console password sync: whoami failed", "err", err)
			return
		}
		h.push(me.ID, password)
	}()
}

func (h *HTTP) Login(ctx context.Context, email, password string) (string, error) {
	sess, err := h.HTTP.Login(ctx, email, password)
	switch ch := err.(type) {
	case nil:
		h.pushSession(sess, password)
	case *core.LoginChallenge:
		h.remember(ch.Challenge, password)
	}
	return sess, err
}

func (h *HTTP) RestoreLogin(ctx context.Context, email, password string) (string, error) {
	sess, err := h.HTTP.RestoreLogin(ctx, email, password)
	switch ch := err.(type) {
	case nil:
		h.pushSession(sess, password)
	case *core.LoginChallenge:
		h.remember(ch.Challenge, password)
	}
	return sess, err
}

func (h *HTTP) Login2FA(ctx context.Context, challenge, code, recovery string) (string, error) {
	sess, err := h.HTTP.Login2FA(ctx, challenge, code, recovery)
	if err == nil {
		if pw := h.take(challenge); pw != "" {
			h.pushSession(sess, pw)
		}
	}
	return sess, err
}

func (h *HTTP) ChangePassword(ctx context.Context, coreSession, current, next string) error {
	if err := h.HTTP.ChangePassword(ctx, coreSession, current, next); err != nil {
		return err
	}
	h.pushSession(coreSession, next)
	return nil
}

// remember keeps the password of a 2FA sign-in in memory only, for the challenge's ten minutes.
func (h *HTTP) remember(challenge, password string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for k, v := range h.pending {
		if now.After(v.expires) {
			delete(h.pending, k)
		}
	}
	h.pending[challenge] = pendingPassword{password, now.Add(10 * time.Minute)}
}

func (h *HTTP) take(challenge string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.pending[challenge]
	delete(h.pending, challenge)
	if !ok || time.Now().After(p.expires) {
		return ""
	}
	return p.password
}
