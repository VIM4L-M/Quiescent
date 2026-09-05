package store

import (
	"errors"
	"testing"
	"time"
)

func TestQueueTriggerThenFetchIt(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)
	sentAt := time.Now().UTC()
	expiresAt := sentAt.Add(20 * time.Hour)

	if err := s.QueueTrigger(ctx, c.CycleID, 2, sentAt, expiresAt); err != nil {
		t.Fatalf("queue trigger: %v", err)
	}

	trig, err := s.TriggerFor(ctx, c.CycleID, 2)
	if err != nil {
		t.Fatalf("trigger for: %v", err)
	}
	if trig == nil {
		t.Fatal("expected a trigger, got nil")
	}
	if trig.Answered() {
		t.Fatal("a freshly queued trigger must not be answered")
	}
	if !trig.ExpiresAt.Equal(expiresAt.Truncate(time.Microsecond)) {
		t.Fatalf("expiresAt: got %s want %s", trig.ExpiresAt, expiresAt)
	}
}

func TestQueueTriggerIsIdempotentPerCycleAndSeq(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)
	sentAt := time.Now().UTC()

	if err := s.QueueTrigger(ctx, c.CycleID, 2, sentAt, sentAt.Add(20*time.Hour)); err != nil {
		t.Fatalf("first queue: %v", err)
	}
	if err := s.QueueTrigger(ctx, c.CycleID, 2, sentAt.Add(time.Hour), sentAt.Add(21*time.Hour)); err != nil {
		t.Fatalf("second queue (must be a harmless no-op): %v", err)
	}

	trig, err := s.TriggerFor(ctx, c.CycleID, 2)
	if err != nil {
		t.Fatalf("trigger for: %v", err)
	}
	if !trig.SentAt.Equal(sentAt.Truncate(time.Microsecond)) {
		t.Fatalf("the second queue call must not have overwritten the first: got sentAt %s want %s", trig.SentAt, sentAt)
	}
}

func TestRespondTriggerRecordsTheAnswerOnce(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)
	sentAt := time.Now().UTC()
	if err := s.QueueTrigger(ctx, c.CycleID, 3, sentAt, sentAt.Add(20*time.Hour)); err != nil {
		t.Fatalf("queue trigger: %v", err)
	}

	respondedAt := sentAt.Add(5 * time.Hour)
	if err := s.RespondTrigger(ctx, c.CycleID, 3, "yes", respondedAt); err != nil {
		t.Fatalf("respond: %v", err)
	}

	trig, err := s.TriggerFor(ctx, c.CycleID, 3)
	if err != nil {
		t.Fatalf("trigger for: %v", err)
	}
	if !trig.Answered() || !trig.SaidYes() {
		t.Fatalf("expected an answered, yes trigger, got %+v", trig)
	}

	if err := s.RespondTrigger(ctx, c.CycleID, 3, "no", respondedAt.Add(time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("responding twice must be rejected as a conflict, got %v", err)
	}
}

func TestPendingTriggerForCycleFindsTheUnansweredOne(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)
	sentAt := time.Now().UTC()

	if err := s.QueueTrigger(ctx, c.CycleID, 2, sentAt, sentAt.Add(20*time.Hour)); err != nil {
		t.Fatalf("queue seq 2: %v", err)
	}
	if err := s.RespondTrigger(ctx, c.CycleID, 2, "no", sentAt.Add(time.Hour)); err != nil {
		t.Fatalf("respond seq 2: %v", err)
	}
	if err := s.QueueTrigger(ctx, c.CycleID, 3, sentAt, sentAt.Add(20*time.Hour)); err != nil {
		t.Fatalf("queue seq 3: %v", err)
	}

	pending, err := s.PendingTriggerForCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("pending trigger: %v", err)
	}
	if pending == nil || pending.Seq != 3 {
		t.Fatalf("expected the unanswered seq=3 trigger, got %+v", pending)
	}
}
