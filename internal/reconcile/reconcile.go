package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/lease"
	"github.com/VIM4L-M/Quiescent/internal/provider"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

const leaseTTL = 5 * time.Minute

type Result string

const (
	ResultNotMyTurn Result = "not_my_turn"
	ResultRecovered Result = "recovered"
	ResultPending   Result = "pending"
	ResultHeld      Result = "held"
)

type Reconciler struct {
	Store    *store.Store
	Bank     *provider.Client
	Holder   string
	Log      *slog.Logger
	MaxTries int
	Backoff  func(try int) time.Duration
}

func New(s *store.Store, bank *provider.Client, holder string, log *slog.Logger) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		Store:    s,
		Bank:     bank,
		Holder:   holder,
		Log:      log,
		MaxTries: 5,
		Backoff:  fullJitterBackoff(1*time.Second, 30*time.Second),
	}
}

func (r *Reconciler) Resolve(ctx context.Context, a domain.Attempt) (Result, error) {
	if _, err := lease.Acquire(ctx, r.Store, a.CycleID, r.Holder, leaseTTL); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return ResultNotMyTurn, nil
		}
		return "", err
	}

	var resp provider.DebitResponse
	var found bool
	for try := 0; try < r.MaxTries; try++ {
		var err error
		resp, found, err = r.Bank.Status(ctx, a.IdempotencyKey)
		if err != nil {
			return "", err
		}
		if found {
			break
		}
		if try < r.MaxTries-1 {
			select {
			case <-time.After(r.Backoff(try)):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}

	if !found {
		if err := r.Store.HoldCycle(ctx, a.CycleID); err != nil {
			return "", err
		}
		r.Log.Warn("still unknown after asking the bank; holding for a human",
			"attemptID", a.AttemptID, "cycleID", a.CycleID, "tries", r.MaxTries)
		r.appendAudit(ctx, a, "held", fmt.Sprintf("still unknown after %d tries against the bank", r.MaxTries))
		return ResultHeld, nil
	}

	if resp.Outcome == domain.OutcomeSuccess {
		if err := r.Store.ResolveDebited(ctx, a.AttemptID); err != nil {
			return "", err
		}
		r.Log.Info("reconciled: debited", "attemptID", a.AttemptID, "cycleID", a.CycleID)
		r.appendAudit(ctx, a, "recovered", "bank confirmed the debit went through")
		return ResultRecovered, nil
	}

	if err := r.Store.ResolveNotDebited(ctx, a.AttemptID); err != nil {
		return "", err
	}
	r.Log.Info("reconciled: not debited, budget refunded",
		"attemptID", a.AttemptID, "cycleID", a.CycleID, "failureCode", resp.FailureCode)
	r.appendAudit(ctx, a, "pending", "bank confirmed the debit never went through; budget refunded")
	return ResultPending, nil
}

func (r *Reconciler) appendAudit(ctx context.Context, a domain.Attempt, resolution, reason string) {
	inputs, err := json.Marshal(map[string]any{
		"idempotencyKey": a.IdempotencyKey,
	})
	if err != nil {
		r.Log.Error("audit: marshal inputs", "attemptID", a.AttemptID, "error", err)
		return
	}
	decision, err := json.Marshal(map[string]any{
		"resolution": resolution,
	})
	if err != nil {
		r.Log.Error("audit: marshal decision", "attemptID", a.AttemptID, "error", err)
		return
	}
	entry := domain.AuditEntry{
		CycleID:       a.CycleID,
		CorrelationID: domain.CorrelationID(a.AttemptID),
		Event:         "attempt_reconciled",
		Inputs:        inputs,
		Decision:      decision,
		Reason:        reason,
	}
	if err := r.Store.AppendAudit(ctx, entry); err != nil {
		r.Log.Error("audit: append", "attemptID", a.AttemptID, "error", err)
	}
}

func fullJitterBackoff(base, cap time.Duration) func(try int) time.Duration {
	return func(try int) time.Duration {
		upper := base << uint(try)
		if upper <= 0 || upper > cap {
			upper = cap
		}
		return time.Duration(rand.Int64N(int64(upper))) + 1
	}
}
