package execute

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/classify"
	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/lease"
	"github.com/VIM4L-M/Quiescent/internal/provider"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

const (
	leaseTTL   = 30 * time.Second
	staleAfter = 10 * time.Minute
)

type Result string

const (
	ResultNotMyTurn     Result = "not_my_turn"
	ResultStale         Result = "stale"
	ResultNoticeMissing Result = "notice_missing"
	ResultFired         Result = "fired"
)

type Worker struct {
	Store  *store.Store
	Bank   *provider.Client
	Holder string
	Log    *slog.Logger
	Now    func() time.Time
}

func New(s *store.Store, bank *provider.Client, holder string, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	return &Worker{
		Store:  s,
		Bank:   bank,
		Holder: holder,
		Log:    log,
		Now:    func() time.Time { return time.Now().UTC() },
	}
}

func (w *Worker) FireOne(ctx context.Context, a domain.Attempt) (Result, error) {
	if w.Now().Sub(a.ScheduledFor) > staleAfter {
		if err := w.Store.AbandonAttempt(ctx, a.AttemptID); err != nil {
			return "", err
		}
		w.Log.Warn("attempt abandoned as stale",
			"attemptID", a.AttemptID, "cycleID", a.CycleID, "scheduledFor", a.ScheduledFor)
		return ResultStale, nil
	}

	handle, err := lease.Acquire(ctx, w.Store, a.CycleID, w.Holder, leaseTTL)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return ResultNotMyTurn, nil
		}
		return "", err
	}

	delivered, err := w.Store.NoticeDelivered(ctx, a.AttemptID, a.ScheduledFor)
	if err != nil {
		return "", err
	}
	if !delivered {
		if err := w.Store.AbandonAttempt(ctx, a.AttemptID); err != nil {
			return "", err
		}
		w.Log.Warn("attempt abandoned, pre-debit notice not delivered",
			"attemptID", a.AttemptID, "cycleID", a.CycleID)
		return ResultNoticeMissing, nil
	}

	cycle, err := w.Store.Cycle(ctx, a.CycleID)
	if err != nil {
		return "", err
	}

	if err := w.Store.MarkAttemptFired(ctx, a.AttemptID, handle.Fence); err != nil {
		return "", err
	}

	resp, err := w.Bank.Debit(ctx, provider.DebitRequest{
		CycleID:        a.CycleID,
		IdempotencyKey: a.IdempotencyKey,
		Fence:          int64(handle.Fence),
		AmountPaise:    cycle.AmountPaise,
		Rail:           cycle.Rail,
	})
	if err != nil {
		w.Log.Warn("debit call did not return cleanly; marking TIMEOUT for reconciliation",
			"attemptID", a.AttemptID, "cycleID", a.CycleID, "error", err)
		if rerr := w.Store.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeTimeout, nil); rerr != nil {
			return "", rerr
		}
		w.appendAudit(ctx, a, domain.OutcomeTimeout, nil, handle.Fence)
		return ResultFired, nil
	}

	var code *domain.FailureCode
	if resp.Outcome == domain.OutcomeFailure {
		code = &resp.FailureCode
	}
	if err := w.Store.RecordAttemptOutcome(ctx, a.AttemptID, resp.Outcome, code); err != nil {
		return "", err
	}
	if err := w.advanceCycle(ctx, a, cycle, resp); err != nil {
		return "", err
	}
	w.appendAudit(ctx, a, resp.Outcome, code, handle.Fence)
	w.Log.Info("attempt fired", "attemptID", a.AttemptID, "cycleID", a.CycleID, "outcome", resp.Outcome)
	return ResultFired, nil
}

func (w *Worker) appendAudit(ctx context.Context, a domain.Attempt, outcome domain.Outcome,
	code *domain.FailureCode, fence domain.Fence) {

	inputs, err := json.Marshal(map[string]any{
		"scheduledFor": a.ScheduledFor,
		"fence":        fence,
	})
	if err != nil {
		w.Log.Error("audit: marshal inputs", "attemptID", a.AttemptID, "error", err)
		return
	}
	decision, err := json.Marshal(map[string]any{
		"outcome":     outcome,
		"failureCode": code,
	})
	if err != nil {
		w.Log.Error("audit: marshal decision", "attemptID", a.AttemptID, "error", err)
		return
	}
	entry := domain.AuditEntry{
		CycleID:       a.CycleID,
		CorrelationID: domain.CorrelationID(a.AttemptID),
		Event:         "attempt_fired",
		Inputs:        inputs,
		Decision:      decision,
		Reason:        "bank returned " + string(outcome),
	}
	if err := w.Store.AppendAudit(ctx, entry); err != nil {
		w.Log.Error("audit: append", "attemptID", a.AttemptID, "error", err)
	}
}

func (w *Worker) advanceCycle(ctx context.Context, a domain.Attempt, cycle domain.MandateCycle, resp provider.DebitResponse) error {
	if resp.Outcome == domain.OutcomeSuccess {
		return w.Store.MarkCycleRecovered(ctx, a.CycleID)
	}
	if resp.Outcome != domain.OutcomeFailure {
		return nil
	}
	class, _ := classify.Classify(resp.FailureCode)
	switch {
	case class == domain.ClassTerminal || class == domain.ClassManualReview:
		return w.Store.EscalateCycle(ctx, a.CycleID)
	case cycle.AttemptsUsed >= int16(domain.MaxAttempts):
		return w.Store.AbandonCycle(ctx, a.CycleID)
	default:
		return w.Store.ReturnCycleToPending(ctx, a.CycleID)
	}
}
