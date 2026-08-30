package schedule_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/schedule"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func testStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		t.Skip("DB_URL not set")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	return s, ctx
}

func seedCycle(t *testing.T, s *store.Store, ctx context.Context, dueDate time.Time) domain.MandateCycle {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   domain.CustomerID(newUUID(t)),
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  50_000,
		DueDate:      dueDate,
		AttemptsUsed: 0,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	return c
}

// realisticDueDate returns midnight UTC on a near-future date — what
// due_date (a DATE column, no time component) actually holds once a cycle
// has round-tripped through Postgres even once. 00:00 UTC = 05:30 IST,
// outside the 10:00-13:00 IST blocked window.
func realisticDueDate() time.Time {
	return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
}

func TestScheduleFirstAttempt(t *testing.T) {
	s, ctx := testStore(t)
	due := realisticDueDate()
	c := seedCycle(t, s, ctx, due)

	sched := schedule.New(s, nil)
	result, err := sched.ScheduleNext(ctx, c, "")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result != schedule.ResultScheduled {
		t.Fatalf("result: got %v want %v", result, schedule.ResultScheduled)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.AttemptsUsed != 1 || got.Version != 1 {
		t.Fatalf("cycle: got attemptsUsed=%d version=%d want 1/1", got.AttemptsUsed, got.Version)
	}

	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Seq != 1 {
		t.Fatalf("expected exactly one seq=1 attempt, got %+v", attempts)
	}
	if !attempts[0].ScheduledFor.Equal(due) {
		t.Fatalf("scheduledFor: got %s want %s (the original attempt fires on the due date)", attempts[0].ScheduledFor, due)
	}
}

func TestScheduleRetryAttempt(t *testing.T) {
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
	if result != schedule.ResultScheduled {
		t.Fatalf("result: got %v want %v", result, schedule.ResultScheduled)
	}

	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	retry := attempts[1]
	if retry.Seq != 2 {
		t.Fatalf("retry seq: got %d want 2", retry.Seq)
	}
	if !retry.ScheduledFor.Equal(due.Add(24 * time.Hour)) {
		t.Fatalf("retry scheduledFor: got %s want %s", retry.ScheduledFor, due.Add(24*time.Hour))
	}
	if retry.DecisionReason.Class != domain.ClassRetryLater {
		t.Fatalf("retry class: got %s want %s", retry.DecisionReason.Class, domain.ClassRetryLater)
	}
}

func TestScheduleNotRetryable(t *testing.T) {
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

	result, err := sched.ScheduleNext(ctx, c, domain.FailureMandateRevoked)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result != schedule.ResultNotRetryable {
		t.Fatalf("result: got %v want %v", result, schedule.ResultNotRetryable)
	}

	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("a non-retryable failure must not add a second attempt, got %d", len(attempts))
	}
	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.AttemptsUsed != 1 {
		t.Fatalf("budget must be untouched when nothing was scheduled, got %d", got.AttemptsUsed)
	}
}

func TestC5ScheduleConcurrentRaceOnlyOneWins(t *testing.T) {
	s, ctx := testStore(t)
	due := realisticDueDate()
	c := seedCycle(t, s, ctx, due) // AttemptsUsed=0, Version=0 — both racers see the same snapshot

	sched := schedule.New(s, nil)

	var wg sync.WaitGroup
	results := make([]schedule.Result, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = sched.ScheduleNext(ctx, c, "")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}

	scheduled, raced := 0, 0
	for _, r := range results {
		switch r {
		case schedule.ResultScheduled:
			scheduled++
		case schedule.ResultBudgetRaced:
			raced++
		default:
			t.Fatalf("unexpected result: %v", r)
		}
	}
	if scheduled != 1 || raced != 1 {
		t.Fatalf("want exactly one scheduled and one raced, got scheduled=%d raced=%d", scheduled, raced)
	}

	got, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.AttemptsUsed != 1 || got.Version != 1 {
		t.Fatalf("two racers must never both win: attemptsUsed=%d version=%d want 1/1", got.AttemptsUsed, got.Version)
	}
	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("exactly one attempt row must exist after the race, got %d", len(attempts))
	}
}
