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

type Result string

const (
	ResultScheduled    Result = "scheduled"
	ResultNotRetryable Result = "not_retryable"
	ResultBudgetRaced  Result = "budget_raced"
)

type Scheduler struct {
	Store *store.Store
	Log   *slog.Logger
}

func New(s *store.Store, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{Store: s, Log: log}
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

	var plan solve.Plan
	if cycle.AttemptsUsed == 0 {
		plan = solve.First(cycle.DueDate, preferredHour)
	} else {
		class, _ := classify.Classify(lastFailureCode)
		var ok bool
		plan, ok = solve.Next(cycle.DueDate, cycle.AttemptsUsed, lastFailureCode, class, preferredHour)
		if !ok {
			s.Log.Info("cycle not retryable", "cycleID", cycle.CycleID, "failureCode", lastFailureCode)
			return ResultNotRetryable, nil
		}
	}

	seq := cycle.AttemptsUsed + 1
	attempt := domain.Attempt{
		AttemptID:      domain.NewAttemptID(),
		CycleID:        cycle.CycleID,
		Seq:            seq,
		IdempotencyKey: domain.NewIdempotencyKey(cycle.CycleID, seq),
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

	err = s.Store.ReserveAttempt(ctx, cycle.CycleID, cycle.Version, attempt, payload, deliverBy)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.Log.Warn("budget reservation raced; cycle changed underneath us",
				"cycleID", cycle.CycleID, "expectedVersion", cycle.Version)
			return ResultBudgetRaced, nil
		}
		return "", err
	}
	s.Log.Info("attempt scheduled",
		"cycleID", cycle.CycleID, "attemptID", attempt.AttemptID, "seq", seq, "scheduledFor", plan.ScheduledFor)
	s.appendAudit(ctx, cycle, attempt, plan, hourBasis, lastFailureCode)
	return ResultScheduled, nil
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
