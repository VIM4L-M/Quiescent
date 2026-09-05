package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func seedFreshCycle(t *testing.T, s *Store, ctx context.Context) domain.MandateCycle {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   domain.CustomerID(newUUID(t)),
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  200000,
		DueDate:      time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		AttemptsUsed: 0,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		t.Fatalf("create fresh cycle: %v", err)
	}
	return c
}

func reserveTestAttempt(t *testing.T, c domain.MandateCycle, seq int16) domain.Attempt {
	t.Helper()
	return domain.Attempt{
		AttemptID:      domain.NewAttemptID(),
		CycleID:        c.CycleID,
		Seq:            seq,
		IdempotencyKey: domain.NewIdempotencyKey(c.CycleID, seq),
		ScheduledFor:   time.Now().UTC().Add(24 * time.Hour),
		DecisionReason: reason(),
	}
}

func TestReserveAttemptSucceedsAndAdvancesBudget(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)

	a := reserveTestAttempt(t, c, 1)
	deliverBy := a.ScheduledFor.Add(-24 * time.Hour)
	_, err := s.ReserveAttempt(ctx, c.CycleID, 0, a, json.RawMessage(`{}`), deliverBy)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.AttemptsUsed != 1 || got.Version != 1 {
		t.Fatalf("cycle: got attemptsUsed=%d version=%d want 1/1", got.AttemptsUsed, got.Version)
	}

	attempt, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if attempt.Fired() {
		t.Fatal("a freshly scheduled attempt must not be fired")
	}

	delivered, err := s.NoticeDelivered(ctx, a.AttemptID, a.ScheduledFor)
	if err != nil {
		t.Fatalf("notice check: %v", err)
	}
	if delivered {
		t.Fatal("the notice was queued, not delivered — it must not read as delivered yet")
	}
	entries, err := s.OutboxByAttempt(ctx, a.AttemptID, domain.OutboxPreDebitNotice)
	if err != nil {
		t.Fatalf("outbox lookup: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one queued notice, got %d", len(entries))
	}
}

func TestC5ReserveAttemptRejectsStaleVersion(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	a := reserveTestAttempt(t, c, 1)
	if _, err := s.ReserveAttempt(ctx, c.CycleID, 999, a, json.RawMessage(`{}`), a.ScheduledFor); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version: want ErrConflict, got %v", err)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.AttemptsUsed != c.AttemptsUsed || got.Version != c.Version {
		t.Fatalf("a rejected reservation must not move the cycle at all, got attemptsUsed=%d version=%d",
			got.AttemptsUsed, got.Version)
	}
	if _, err := s.Attempt(ctx, a.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no attempt row should exist after a rejected reservation, got %v", err)
	}
}

func TestC7ReserveAttemptLeavesNothingBehindOnFailure(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)

	bad := reserveTestAttempt(t, c, 1)
	bad.DecisionReason.Class = "not-a-real-class"

	_, err := s.ReserveAttempt(ctx, c.CycleID, 0, bad, json.RawMessage(`{}`), bad.ScheduledFor)
	if err == nil {
		t.Fatal("expected the attempt insert to fail its own validation")
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.AttemptsUsed != 0 || got.Version != 0 {
		t.Fatalf("budget must not be burned when the attempt insert fails: attemptsUsed=%d version=%d",
			got.AttemptsUsed, got.Version)
	}
}

func TestC10SeqSurvivesAnAbandonAndRefund(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)

	first := reserveTestAttempt(t, c, 1)
	first, err := s.ReserveAttempt(ctx, c.CycleID, 0, first, json.RawMessage(`{}`), first.ScheduledFor)
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if first.Seq != 1 {
		t.Fatalf("first attempt: got seq %d want 1", first.Seq)
	}

	if err := s.AbandonAttempt(ctx, first.AttemptID); err != nil {
		t.Fatalf("abandon first: %v", err)
	}
	refunded, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle after abandon: %v", err)
	}
	if refunded.AttemptsUsed != 0 || refunded.State != domain.StatePending {
		t.Fatalf("abandon must refund budget and return to pending: attemptsUsed=%d state=%q",
			refunded.AttemptsUsed, refunded.State)
	}

	second := reserveTestAttempt(t, c, 1)
	second, err = s.ReserveAttempt(ctx, c.CycleID, refunded.Version, second, json.RawMessage(`{}`), second.ScheduledFor)
	if err != nil {
		t.Fatalf("reserve second (must not collide with the abandoned attempt's idempotency key): %v", err)
	}
	if second.Seq != 2 {
		t.Fatalf("second attempt: got seq %d want 2 — a permanently-taken idempotency key must never be reused", second.Seq)
	}
	if second.IdempotencyKey == first.IdempotencyKey {
		t.Fatal("second attempt reused the first attempt's idempotency key")
	}
}

func TestReserveAttemptRejectsWhenBudgetExhausted(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	c.AttemptsUsed = int16(domain.MaxAttempts)
	c.Version = 5
	if err := s.pool.QueryRow(ctx,
		`UPDATE mandate_cycles SET attempts_used = $2, version = $3 WHERE cycle_id = $1 RETURNING attempts_used`,
		c.CycleID, c.AttemptsUsed, c.Version).Scan(new(int16)); err != nil {
		t.Fatalf("seed exhausted budget: %v", err)
	}

	a := reserveTestAttempt(t, c, domain.MaxAttempts+1)
	if _, err := s.ReserveAttempt(ctx, c.CycleID, c.Version, a, json.RawMessage(`{}`), a.ScheduledFor); !errors.Is(err, ErrConflict) {
		t.Fatalf("exhausted budget: want ErrConflict, got %v", err)
	}
}
