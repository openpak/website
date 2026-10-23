// Package erasure finishes account deletion automatically (owner decision
// 2026-10-23). Deleting an account closes it at once in the core; once its
// grace period is over (the core's ACCOUNT_DELETION_GRACE, 30 days) this job
// erases it in every service, and only when every service has succeeded does
// it ask the core to finalize, which is the point the account counts as
// erased. A failed step is logged and retried on the next run; every step is
// idempotent, so a retry repeats the ones that already worked harmlessly.
//
// Within the grace period the owner restores by signing in and an admin from
// the users page; the core refuses a restore once the date has passed, so
// nothing can come back half-erased.
package erasure

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Step erases one service's data about an account. It must be idempotent: an
// account with nothing left there is a success.
type Step struct {
	Name  string
	Erase func(ctx context.Context, accountID string) error
}

// Core is the account core's side: who is due, and the last step.
type Core interface {
	DueDeletions(ctx context.Context, limit int) ([]string, error)
	FinalizeDeletion(ctx context.Context, accountID string) error
}

// Locker is a lease held by one website instance at a time.
type Locker interface {
	TryLock(ctx context.Context, name string) (unlock func(), ok bool, err error)
}

const (
	lockName = "openpak.account-erasure"
	pageSize = 100
)

// Finaliser runs the steps for every due account, then finalizes it.
type Finaliser struct {
	Core     Core
	Steps    []Step
	Lock     Locker
	Interval time.Duration // between runs; default one hour
	Log      *slog.Logger
}

// Result counts one run.
type Result struct {
	Due, Erased, Failed int
}

// RunOnce handles every account that is due now. Another instance holding
// the lease makes it a no-op.
func (f *Finaliser) RunOnce(ctx context.Context) (Result, error) {
	var res Result
	unlock, ok, err := f.Lock.TryLock(ctx, lockName)
	if err != nil {
		return res, err
	}
	if !ok {
		f.log().Info("erasure: another instance holds the lease; skipping this run")
		return res, nil
	}
	defer unlock()
	for {
		ids, err := f.Core.DueDeletions(ctx, pageSize)
		if err != nil {
			return res, err
		}
		erased := 0
		for _, id := range ids {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			res.Due++
			if f.erase(ctx, id) {
				erased++
			} else {
				res.Failed++
			}
		}
		res.Erased += erased
		// A failed account stays due and comes back first: a full page that
		// erased nothing would only repeat itself, so it waits for the next run.
		if len(ids) < pageSize || erased == 0 {
			return res, nil
		}
	}
}

// erase runs every step for one account, then finalizes it if all succeeded.
func (f *Finaliser) erase(ctx context.Context, id string) bool {
	failed := 0
	for _, st := range f.Steps {
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := st.Erase(c, id)
		cancel()
		if err != nil {
			failed++
			f.log().Warn("erasure: step failed; will retry", "account", id, "service", st.Name, "err", err)
			continue
		}
		f.log().Info("erasure: step done", "account", id, "service", st.Name)
	}
	if failed > 0 {
		f.log().Warn("erasure: account not finalized", "account", id, "failed_steps", failed)
		return false
	}
	if err := f.Core.FinalizeDeletion(ctx, id); err != nil {
		f.log().Warn("erasure: core finalize failed; will retry", "account", id, "err", err)
		return false
	}
	f.log().Info("erasure: account erased everywhere", "account", id)
	return true
}

// Run loops until ctx ends: one run at start, then every Interval.
func (f *Finaliser) Run(ctx context.Context) {
	every := f.Interval
	if every <= 0 {
		every = time.Hour
	}
	for {
		res, err := f.RunOnce(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return
		case err != nil:
			f.log().Warn("erasure: run failed", "err", err)
		case res.Due > 0:
			f.log().Info("erasure: run", "due", res.Due, "erased", res.Erased, "failed", res.Failed)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (f *Finaliser) log() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}
