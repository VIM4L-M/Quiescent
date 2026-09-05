package store

import (
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func TestFailureCodeStatsAggregatesAcrossCycles(t *testing.T) {
	s, ctx := testStore(t)
	code := domain.FailureCode("WEIRD_CODE_" + newUUID(t)[:8])

	recoveredCycle := seedCycle(t, s, ctx)
	recoveredAttempt := seedAttempt(t, s, ctx, recoveredCycle, 1)
	if err := s.MarkAttemptFired(ctx, recoveredAttempt.AttemptID, domain.Fence(1)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, recoveredAttempt.AttemptID, domain.OutcomeFailure, codePtr(code)); err != nil {
		t.Fatalf("record outcome: %v", err)
	}
	if err := s.MarkCycleRecovered(ctx, recoveredCycle.CycleID); err != nil {
		t.Fatalf("mark recovered: %v", err)
	}

	terminalCycle := seedCycle(t, s, ctx)
	terminalAttempt := seedAttempt(t, s, ctx, terminalCycle, 1)
	if err := s.MarkAttemptFired(ctx, terminalAttempt.AttemptID, domain.Fence(1)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, terminalAttempt.AttemptID, domain.OutcomeFailure, codePtr(code)); err != nil {
		t.Fatalf("record outcome: %v", err)
	}
	if err := s.EscalateCycle(ctx, terminalCycle.CycleID); err != nil {
		t.Fatalf("escalate cycle: %v", err)
	}

	stats, err := s.FailureCodeStats(ctx, 1, 10)
	if err != nil {
		t.Fatalf("failure code stats: %v", err)
	}
	var got *domain.FailureCodeStat
	for i := range stats {
		if stats[i].Code == code {
			got = &stats[i]
		}
	}
	if got == nil {
		t.Fatalf("expected a stat row for %q, got %+v", code, stats)
	}
	if got.Occurrences != 2 {
		t.Fatalf("occurrences: got %d want 2", got.Occurrences)
	}
	if got.Recovered != 1 {
		t.Fatalf("recovered: got %d want 1", got.Recovered)
	}
	if got.Terminal != 1 {
		t.Fatalf("terminal: got %d want 1", got.Terminal)
	}
}

func TestFailureCodeStatsRespectsMinOccurrences(t *testing.T) {
	s, ctx := testStore(t)
	code := domain.FailureCode("RARE_CODE_" + newUUID(t)[:8])

	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 1)
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(1)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeFailure, codePtr(code)); err != nil {
		t.Fatalf("record outcome: %v", err)
	}

	stats, err := s.FailureCodeStats(ctx, 5, 10)
	if err != nil {
		t.Fatalf("failure code stats: %v", err)
	}
	for _, stat := range stats {
		if stat.Code == code {
			t.Fatalf("a code seen once must not appear when minOccurrences=5, got %+v", stat)
		}
	}
}
