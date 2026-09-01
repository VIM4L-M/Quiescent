package store

import (
	"context"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func seedSuccessfulAttempt(t *testing.T, s *Store, ctx context.Context, customerID domain.CustomerID, firedAt time.Time) {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   customerID,
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  50_000,
		DueDate:      time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		AttemptsUsed: 1,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	a := seedAttempt(t, s, ctx, c, 1)
	_, err := s.pool.Exec(ctx,
		`UPDATE attempts SET fired_at = $2, outcome = 'SUCCESS' WHERE attempt_id = $1`,
		a.AttemptID, firedAt)
	if err != nil {
		t.Fatalf("seed successful attempt: %v", err)
	}
}

func TestCustomerSuccessHistoryReturnsPastSuccessesMostRecentFirst(t *testing.T) {
	s, ctx := testStore(t)
	customerID := domain.CustomerID(newUUID(t))

	older := time.Date(2026, 1, 5, 14, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 2, 5, 14, 0, 0, 0, time.UTC)
	seedSuccessfulAttempt(t, s, ctx, customerID, older)
	seedSuccessfulAttempt(t, s, ctx, customerID, newer)

	// a different customer's success must never leak into this customer's history
	seedSuccessfulAttempt(t, s, ctx, domain.CustomerID(newUUID(t)), time.Now().UTC())

	history, err := s.CustomerSuccessHistory(ctx, customerID, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history: got %d entries want 2", len(history))
	}
	if !history[0].Equal(newer) || !history[1].Equal(older) {
		t.Fatalf("history should be most-recent-first: got %v, %v", history[0], history[1])
	}
}

func TestCustomerSuccessHistoryIgnoresFailuresAndUnresolvedAttempts(t *testing.T) {
	s, ctx := testStore(t)
	customerID := domain.CustomerID(newUUID(t))
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   customerID,
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  50_000,
		DueDate:      time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		AttemptsUsed: 1,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		t.Fatalf("create cycle: %v", err)
	}

	unfired := seedAttempt(t, s, ctx, c, 1)
	_ = unfired // never fired, never resolved — must not count

	fired := seedAttempt(t, s, ctx, c, 2)
	if err := s.MarkAttemptFired(ctx, fired.AttemptID, domain.Fence(1)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	code := domain.FailureInsufficientFunds
	if err := s.RecordAttemptOutcome(ctx, fired.AttemptID, domain.OutcomeFailure, &code); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	history, err := s.CustomerSuccessHistory(ctx, customerID, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("an unfired attempt and a FAILURE outcome must not count as success history, got %d entries", len(history))
	}
}
