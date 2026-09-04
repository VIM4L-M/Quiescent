package store

import (
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func TestReserveAttemptSetsCycleScheduled(t *testing.T) {
	s, ctx := testStore(t)
	c := seedFreshCycle(t, s, ctx)
	a := reserveTestAttempt(t, c, 1)

	if err := s.ReserveAttempt(ctx, c.CycleID, 0, a, []byte(`{}`), a.ScheduledFor); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.State != domain.StateScheduled {
		t.Fatalf("state: got %q want %q", got.State, domain.StateScheduled)
	}
}

func TestMarkAttemptFiredSetsCycleInFlight(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 1)

	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(3)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.State != domain.StateInFlight {
		t.Fatalf("state: got %q want %q", got.State, domain.StateInFlight)
	}
}

func TestRecordTimeoutSetsCycleUnknown(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 1)
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(3)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}

	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeTimeout, nil); err != nil {
		t.Fatalf("record timeout: %v", err)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.State != domain.StateUnknown {
		t.Fatalf("state: got %q want %q — this is the signal reconciliation and the scheduler both key off",
			got.State, domain.StateUnknown)
	}
}

func TestRecordFailureLeavesCycleStateForExecuteToDecide(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 1)
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(3)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}

	code := domain.FailureInsufficientFunds
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeFailure, &code); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.State != domain.StateInFlight {
		t.Fatalf("state: got %q want %q — store records what the bank said, "+
			"it does not decide retry vs escalate vs abandon; that needs classify and the budget, which live in execute",
			got.State, domain.StateInFlight)
	}
}

func TestD6SchedulableCyclesExcludesCycleStuckInUnknown(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 1)
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(3)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeTimeout, nil); err != nil {
		t.Fatalf("record timeout: %v", err)
	}

	schedulable, err := s.SchedulableCycles(ctx, 10)
	if err != nil {
		t.Fatalf("schedulable cycles: %v", err)
	}
	for _, sc := range schedulable {
		if sc.CycleID == c.CycleID {
			t.Fatal("D6: a cycle stuck in unknown, awaiting reconciliation, must never be re-scheduled")
		}
	}
}

func TestD6SchedulableCyclesCatchesUnresolvedAttemptEvenIfStateWasWronglyPending(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx) // state stays pending — the exact condition this project shipped with before D6 was found
	seedAttempt(t, s, ctx, c, 1)

	naive, err := s.CyclesByState(ctx, domain.StatePending, 10)
	if err != nil {
		t.Fatalf("naive query: %v", err)
	}
	foundInNaive := false
	for _, nc := range naive {
		if nc.CycleID == c.CycleID {
			foundInNaive = true
		}
	}
	if !foundInNaive {
		t.Fatal("setup broken: the naive CyclesByState(pending) query should have found this cycle — " +
			"that inclusion is the D6 bug being demonstrated")
	}

	fixed, err := s.SchedulableCycles(ctx, 10)
	if err != nil {
		t.Fatalf("schedulable cycles: %v", err)
	}
	for _, fc := range fixed {
		if fc.CycleID == c.CycleID {
			t.Fatal("D6: SchedulableCycles must exclude a cycle with an unresolved attempt, " +
				"even when the state column itself is wrong")
		}
	}
}
