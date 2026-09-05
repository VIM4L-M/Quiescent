package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/classify"
	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/predict"
	"github.com/VIM4L-M/Quiescent/internal/solve"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

const historyLimit = 20
const triggerTimeout = 20 * time.Hour

type Result string

const (
	ResultScheduled       Result = "scheduled"
	ResultNotRetryable    Result = "not_retryable"
	ResultBudgetRaced     Result = "budget_raced"
	ResultAwaitingTrigger Result = "awaiting_trigger"
)

type Scheduler struct {
	Store *store.Store
	Log   *slog.Logger
	Now   func() time.Time
}

func New(s *store.Store, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{Store: s, Log: log, Now: func() time.Time { return time.Now().UTC() }}
}

type notice struct {
	AmountPaise  int64     `json:"amountPaise"`
	Rail         string    `json:"rail"`
	FiredNoLater time.Time `json:"firedNoLaterThan"`
}

func (s *Scheduler) ScheduleNext(ctx context.Context, cycle domain.MandateCycle, lastFailureCode domain.FailureCode) (Result, error) {
	history, err := s.Store.CustomerSuccessHistory(ctx, cycle.CustomerID, historyLimit)
	if err != nil {
		return "", err
	}
	preferredHour, hourBasis := predict.PreferredHour(history)
	s.Log.Info("history-derived hour preference",
		"cycleID", cycle.CycleID, "customerID", cycle.CustomerID, "basis", hourBasis)

	now := s.Now()
	var plan solve.Plan
	switch {
	case cycle.AttemptsUsed == 0:
		plan = solve.First(cycle.DueDate, preferredHour, now)
	case lastFailureCode == domain.FailureInsufficientFunds:
		p, result, err := s.gateOnBalanceTrigger(ctx, cycle, cycle.AttemptsUsed+1, preferredHour, now)
		if err != nil {
			return "", err
		}
		if p == nil {
			return result, nil
		}
		plan = *p
	default:
		class, _ := classify.Classify(lastFailureCode)
		var ok bool
		plan, ok = solve.Next(cycle.DueDate, cycle.AttemptsUsed, lastFailureCode, class, preferredHour, now)
		if !ok {
			s.Log.Info("cycle not retryable", "cycleID", cycle.CycleID, "failureCode", lastFailureCode)
			return ResultNotRetryable, nil
		}
	}

	attempt := domain.Attempt{
		AttemptID:      domain.NewAttemptID(),
		CycleID:        cycle.CycleID,
		ScheduledFor:   plan.ScheduledFor,
		DecisionReason: plan.Reason,
	}

	deliverBy, err := time.Parse(time.RFC3339, plan.Reason.Constraints.NoticeDeadline)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(notice{
		AmountPaise:  cycle.AmountPaise,
		Rail:         string(cycle.Rail),
		FiredNoLater: plan.ScheduledFor,
	})
	if err != nil {
		return "", err
	}

	attempt, err = s.Store.ReserveAttempt(ctx, cycle.CycleID, cycle.Version, attempt, payload, deliverBy)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.Log.Warn("budget reservation raced; cycle changed underneath us",
				"cycleID", cycle.CycleID, "expectedVersion", cycle.Version)
			return ResultBudgetRaced, nil
		}
		return "", err
	}
	s.Log.Info("attempt scheduled",
		"cycleID", cycle.CycleID, "attemptID", attempt.AttemptID, "seq", attempt.Seq, "scheduledFor", plan.ScheduledFor)
	s.appendAudit(ctx, cycle, attempt, plan, hourBasis, lastFailureCode)
	return ResultScheduled, nil
}

func (s *Scheduler) gateOnBalanceTrigger(ctx context.Context, cycle domain.MandateCycle, seq int16,
	preferredHour *int, now time.Time) (*solve.Plan, Result, error) {

	trig, err := s.Store.TriggerFor(ctx, cycle.CycleID, seq)
	if err != nil {
		return nil, "", err
	}

	if trig == nil {
		expiresAt := now.Add(triggerTimeout)
		if err := s.Store.QueueTrigger(ctx, cycle.CycleID, seq, now, expiresAt); err != nil {
			return nil, "", err
		}
		s.Log.Info("balance-check trigger sent",
			"cycleID", cycle.CycleID, "seq", seq, "expiresAt", expiresAt)
		return nil, ResultAwaitingTrigger, nil
	}

	if trig.SaidYes() {
		plan := solve.AfterConfirmation(*trig.RespondedAt, domain.FailureInsufficientFunds, cycle.AttemptsUsed)
		s.Log.Info("balance-check trigger confirmed — firing early",
			"cycleID", cycle.CycleID, "seq", seq, "scheduledFor", plan.ScheduledFor)
		return &plan, "", nil
	}

	if trig.Answered() || trig.Expired(now) {
		// explicit "no", or the window ran out with no reply — fall back to
		// exactly the same fixed retry the system would have scheduled with
		// no trigger at all. We can never tell a genuine "no" apart from a
		// wrong one, so the safe default is to try anyway either way.
		class, _ := classify.Classify(domain.FailureInsufficientFunds)
		plan, ok := solve.Next(cycle.DueDate, cycle.AttemptsUsed, domain.FailureInsufficientFunds, class, preferredHour, now)
		if !ok {
			return nil, ResultNotRetryable, nil
		}
		return &plan, "", nil
	}

	return nil, ResultAwaitingTrigger, nil
}

func (s *Scheduler) appendAudit(ctx context.Context, cycle domain.MandateCycle, attempt domain.Attempt,
	plan solve.Plan, hourBasis string, lastFailureCode domain.FailureCode) {

	inputs, err := json.Marshal(map[string]any{
		"attemptsUsed":    cycle.AttemptsUsed,
		"lastFailureCode": lastFailureCode,
		"hourBasis":       hourBasis,
	})
	if err != nil {
		s.Log.Error("audit: marshal inputs", "cycleID", cycle.CycleID, "error", err)
		return
	}
	decision, err := json.Marshal(plan.Reason)
	if err != nil {
		s.Log.Error("audit: marshal decision", "cycleID", cycle.CycleID, "error", err)
		return
	}
	entry := domain.AuditEntry{
		CycleID:       cycle.CycleID,
		CorrelationID: domain.CorrelationID(attempt.AttemptID),
		Event:         "attempt_scheduled",
		Inputs:        inputs,
		Decision:      decision,
		Reason:        plan.Reason.PredictionBasis,
	}
	if err := s.Store.AppendAudit(ctx, entry); err != nil {
		s.Log.Error("audit: append", "cycleID", cycle.CycleID, "attemptID", attempt.AttemptID, "error", err)
	}
}
