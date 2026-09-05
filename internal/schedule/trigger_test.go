package schedule_test

import (
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/schedule"
)

func TestC12FundsRelatedRetryAsksForConfirmationInstead(t *testing.T) {
	s, ctx := testStore(t)
	due := realisticDueDate()
	c := seedCycle(t, s, ctx, due)

	sched := schedule.New(s, nil)
	if _, err := sched.ScheduleNext(ctx, c, ""); err != nil {
		t.Fatalf("schedule first: %v", err)
	}
	c, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("reload cycle: %v", err)
	}

	result, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds)
	if err != nil {
		t.Fatalf("schedule retry: %v", err)
	}
	if result != schedule.ResultAwaitingTrigger {
		t.Fatalf("result: got %v want %v", result, schedule.ResultAwaitingTrigger)
	}

	trig, err := s.TriggerFor(ctx, c.CycleID, 2)
	if err != nil {
		t.Fatalf("trigger for: %v", err)
	}
	if trig == nil {
		t.Fatal("expected a balance-check trigger to have been queued")
	}
	if trig.Answered() {
		t.Fatal("a freshly queued trigger must not be answered")
	}

	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("no second attempt should be reserved while awaiting a trigger response, got %d attempts", len(attempts))
	}
}

func TestC12ConfirmedYesFiresEarlyInsteadOfWaitingForTheFixedSlot(t *testing.T) {
	s, ctx := testStore(t)
	due := realisticDueDate()
	c := seedCycle(t, s, ctx, due)

	sched := schedule.New(s, nil)
	if _, err := sched.ScheduleNext(ctx, c, ""); err != nil {
		t.Fatalf("schedule first: %v", err)
	}
	c, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if _, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds); err != nil {
		t.Fatalf("first tick, queues the trigger: %v", err)
	}

	respondedAt := time.Now().UTC().Add(3 * time.Hour)
	if err := s.RespondTrigger(ctx, c.CycleID, 2, "yes", respondedAt); err != nil {
		t.Fatalf("respond yes: %v", err)
	}

	result, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds)
	if err != nil {
		t.Fatalf("schedule after yes: %v", err)
	}
	if result != schedule.ResultScheduled {
		t.Fatalf("result: got %v want %v", result, schedule.ResultScheduled)
	}

	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected the confirmed retry to be reserved, got %d attempts", len(attempts))
	}
	retry := attempts[1]
	wantNoEarlierThan := respondedAt.Add(24 * time.Hour).Truncate(time.Microsecond)
	if retry.ScheduledFor.Before(wantNoEarlierThan) {
		t.Fatalf("scheduledFor %s must respect the 24h notice lead from the yes at %s", retry.ScheduledFor, respondedAt)
	}
	if domain.Blocked(retry.ScheduledFor) {
		t.Fatalf("scheduledFor %s must not fall inside a blocked window", retry.ScheduledFor)
	}
}

func TestC12NoResponseStillFiresOnTheNormalScheduleOnceExpired(t *testing.T) {
	s, ctx := testStore(t)
	due := realisticDueDate()
	c := seedCycle(t, s, ctx, due)

	sched := schedule.New(s, nil)
	fixedNow := time.Now().UTC()
	sched.Now = func() time.Time { return fixedNow }

	if _, err := sched.ScheduleNext(ctx, c, ""); err != nil {
		t.Fatalf("schedule first: %v", err)
	}
	c, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if _, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds); err != nil {
		t.Fatalf("first tick, queues the trigger: %v", err)
	}

	stillWaiting, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds)
	if err != nil {
		t.Fatalf("second tick, before expiry: %v", err)
	}
	if stillWaiting != schedule.ResultAwaitingTrigger {
		t.Fatalf("before the trigger expires with no response: got %v want %v", stillWaiting, schedule.ResultAwaitingTrigger)
	}

	sched.Now = func() time.Time { return fixedNow.Add(21 * time.Hour) }
	result, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds)
	if err != nil {
		t.Fatalf("schedule after expiry: %v", err)
	}
	if result != schedule.ResultScheduled {
		t.Fatalf("result: got %v want %v — silence must still fall back to the normal fixed retry", result, schedule.ResultScheduled)
	}

	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected the fallback retry to be reserved, got %d attempts", len(attempts))
	}
}

func TestC12ExplicitNoAlsoFallsBackToTheNormalSchedule(t *testing.T) {
	s, ctx := testStore(t)
	due := realisticDueDate()
	c := seedCycle(t, s, ctx, due)

	sched := schedule.New(s, nil)
	if _, err := sched.ScheduleNext(ctx, c, ""); err != nil {
		t.Fatalf("schedule first: %v", err)
	}
	c, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("reload cycle: %v", err)
	}
	if _, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds); err != nil {
		t.Fatalf("first tick, queues the trigger: %v", err)
	}
	if err := s.RespondTrigger(ctx, c.CycleID, 2, "no", time.Now().UTC()); err != nil {
		t.Fatalf("respond no: %v", err)
	}

	result, err := sched.ScheduleNext(ctx, c, domain.FailureInsufficientFunds)
	if err != nil {
		t.Fatalf("schedule after no: %v", err)
	}
	if result != schedule.ResultScheduled {
		t.Fatalf("result: got %v want %v — an explicit no must still fall back to the normal fixed retry, same as silence", result, schedule.ResultScheduled)
	}
}

func TestC12NonFundsFailureNeverGetsATrigger(t *testing.T) {
	s, ctx := testStore(t)
	due := realisticDueDate()
	c := seedCycle(t, s, ctx, due)

	sched := schedule.New(s, nil)
	if _, err := sched.ScheduleNext(ctx, c, ""); err != nil {
		t.Fatalf("schedule first: %v", err)
	}
	c, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("reload cycle: %v", err)
	}

	result, err := sched.ScheduleNext(ctx, c, "TECHNICAL_DECLINE")
	if err != nil {
		t.Fatalf("schedule retry: %v", err)
	}
	if result != schedule.ResultScheduled {
		t.Fatalf("result: got %v want %v — a non-funds failure must behave exactly as before", result, schedule.ResultScheduled)
	}

	trig, err := s.TriggerFor(ctx, c.CycleID, 2)
	if err != nil {
		t.Fatalf("trigger for: %v", err)
	}
	if trig != nil {
		t.Fatal("a non-funds-related retry must never queue a balance-check trigger")
	}
}
