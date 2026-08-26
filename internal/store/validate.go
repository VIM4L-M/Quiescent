package store

import (
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func validateCycle(c domain.MandateCycle) error {
	if c.CycleID == "" || c.MandateID == "" || c.CustomerID == "" {
		return fmt.Errorf("%w: cycleID, mandateID and customerID are required", ErrInvalidArgument)
	}
	if !c.Rail.Valid() {
		return fmt.Errorf("%w: rail %q", ErrInvalidEnum, c.Rail)
	}
	if !c.State.Valid() {
		return fmt.Errorf("%w: state %q", ErrInvalidEnum, c.State)
	}
	if c.AmountPaise <= 0 {
		return fmt.Errorf("%w: amountPaise must be positive", ErrInvalidArgument)
	}
	if c.DueDate.IsZero() {
		return fmt.Errorf("%w: dueDate is required", ErrInvalidArgument)
	}
	if c.AttemptsUsed < 0 || c.AttemptsUsed > domain.MaxAttempts {
		return fmt.Errorf("%w: attemptsUsed %d outside 0..%d",
			ErrInvalidArgument, c.AttemptsUsed, domain.MaxAttempts)
	}
	return nil
}

func validateAttempt(a domain.Attempt) error {
	if a.AttemptID == "" || a.CycleID == "" {
		return fmt.Errorf("%w: attemptID and cycleID are required", ErrInvalidArgument)
	}
	if a.Seq < 1 || a.Seq > domain.MaxAttempts {
		return fmt.Errorf("%w: seq %d outside 1..%d", ErrInvalidArgument, a.Seq, domain.MaxAttempts)
	}
	if a.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotencyKey is required", ErrInvalidArgument)
	}
	if a.ScheduledFor.IsZero() {
		return fmt.Errorf("%w: scheduledFor is required", ErrInvalidArgument)
	}
	if a.Fence != nil || a.FiredAt != nil || a.Outcome != nil || a.FailureCode != nil {
		return fmt.Errorf("%w: fence, firedAt, outcome and failureCode are set at fire time",
			ErrWriteAheadViolation)
	}
	if !a.DecisionReason.Class.Valid() {
		return fmt.Errorf("%w: decisionReason.class %q", ErrInvalidEnum, a.DecisionReason.Class)
	}
	if !a.DecisionReason.ClassifiedBy.Valid() {
		return fmt.Errorf("%w: decisionReason.classifiedBy %q", ErrInvalidEnum, a.DecisionReason.ClassifiedBy)
	}
	return nil
}

func validateOutcome(o domain.Outcome, code *domain.FailureCode) error {
	if !o.Valid() {
		return fmt.Errorf("%w: outcome %q", ErrInvalidEnum, o)
	}
	if o == domain.OutcomeFailure && code == nil {
		return fmt.Errorf("%w: FAILURE requires a failure code", ErrInvalidArgument)
	}
	if o != domain.OutcomeFailure && code != nil {
		return fmt.Errorf("%w: %s must not carry a failure code", ErrInvalidArgument, o)
	}
	if code != nil && *code == "" {
		return fmt.Errorf("%w: failure code must not be empty", ErrInvalidArgument)
	}
	return nil
}

func validateAudit(e domain.AuditEntry) error {
	if e.CycleID == "" || e.CorrelationID == "" {
		return fmt.Errorf("%w: cycleID and correlationID are required", ErrInvalidArgument)
	}
	if e.Event == "" {
		return fmt.Errorf("%w: event is required", ErrInvalidArgument)
	}
	if e.Reason == "" {
		return fmt.Errorf("%w: reason is required", ErrInvalidArgument)
	}
	if len(e.Inputs) == 0 || len(e.Decision) == 0 {
		return fmt.Errorf("%w: inputs and decision must be JSON documents", ErrInvalidArgument)
	}
	return nil
}
