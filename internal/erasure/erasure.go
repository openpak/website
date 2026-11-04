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
	"sync"
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
	// Expected names every service that must have a step (ExpectedServices).
	// One with no step is logged on each erasure and shown on the admin page.
	Expected []string

	mu   sync.Mutex
	last map[string]StepReport
}

// StepReport is one service's standing, for the admin page.
type StepReport struct {
	Name       string
	Configured bool
	Ran        bool // has run since this process started
	OK         bool
	At         time.Time
	Err        string
}

// Report lists every expected service, then any extra step, with the result
// of its latest run.
func (f *Finaliser) Report() []StepReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	have := map[string]bool{}
	for _, st := range f.Steps {
		have[st.Name] = true
	}
	var out []StepReport
	add := func(n string) {
		r := f.last[n]
		r.Name, r.Configured = n, have[n]
		out = append(out, r)
	}
	seen := map[string]bool{}
	for _, n := range f.Expected {
		seen[n] = true
		add(n)
	}
	for _, st := range f.Steps {
		if !seen[st.Name] {
			seen[st.Name] = true
			add(st.Name)
		}
	}
	return out
}

func (f *Finaliser) record(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = map[string]StepReport{}
	}
	r := StepReport{Ran: true, OK: err == nil, At: time.Now()}
	if err != nil {
		r.Err = err.Error()
	}
	f.last[name] = r
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
	for _, m := range f.missing() {
		f.log().Warn("erasure: service has no erase step configured; its data for this account is NOT erased", "account", id, "service", m)
	}
	for _, st := range f.Steps {
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := st.Erase(c, id)
		cancel()
		f.record(st.Name, err)
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

// missing lists expected services with no step.
func (f *Finaliser) missing() []string {
	have := map[string]bool{}
	for _, st := range f.Steps {
		have[st.Name] = true
	}
	var out []string
	for _, n := range f.Expected {
		if !have[n] {
			out = append(out, n)
		}
	}
	return out
}
