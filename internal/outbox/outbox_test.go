package outbox_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/outbox"
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

func seedCycle(t *testing.T, s *store.Store, ctx context.Context) domain.MandateCycle {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   domain.CustomerID(newUUID(t)),
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  50_000,
		DueDate:      time.Now().UTC(),
		AttemptsUsed: 1,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	return c
}

func reason() domain.DecisionReason {
	return domain.DecisionReason{
		FailureCode:     domain.FailureInsufficientFunds,
		Class:           domain.ClassRetryLater,
		ClassifiedBy:    domain.ClassifiedByTable,
		PredictedFunds:  "n/a",
		PredictionBasis: "test fixture",
		Constraints: domain.ReasonConstraints{
			BlockedWindowShift: "n/a",
			NoticeDeadline:     "n/a",
			RailRules:          "upi_autopay",
		},
		BudgetBefore: 1,
		BudgetAfter:  2,
	}
}

func seedAttemptWithNotice(t *testing.T, s *store.Store, ctx context.Context, c domain.MandateCycle,
	scheduledFor, deliverBy time.Time) (domain.Attempt, domain.OutboxEntry) {
	t.Helper()

	a := domain.Attempt{
		AttemptID:      domain.NewAttemptID(),
		CycleID:        c.CycleID,
		Seq:            2,
		IdempotencyKey: domain.NewIdempotencyKey(c.CycleID, 2),
		ScheduledFor:   scheduledFor,
		DecisionReason: reason(),
	}
	if err := s.InsertAttempt(ctx, a); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	if err := s.QueueNotice(ctx, c.CycleID, a.AttemptID, domain.OutboxPreDebitNotice,
		json.RawMessage(`{"amountPaise":50000}`), deliverBy); err != nil {
		t.Fatalf("queue notice: %v", err)
	}
	entries, err := s.OutboxByAttempt(ctx, a.AttemptID, domain.OutboxPreDebitNotice)
	if err != nil || len(entries) != 1 {
		t.Fatalf("load queued notice: entries=%v err=%v", entries, err)
	}
	return a, entries[0]
}

type spySender struct {
	called bool
	err    error
}

func (s *spySender) Send(ctx context.Context, cycleID domain.CycleID, attemptID domain.AttemptID,
	kind domain.OutboxKind, payload json.RawMessage) error {
	s.called = true
	return s.err
}

func TestProcessOneDeliversWithinDeadline(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	scheduledFor := time.Now().UTC().Add(30 * time.Hour)
	deliverBy := scheduledFor.Add(-24 * time.Hour) // 6h from now — plenty of time
	a, entry := seedAttemptWithNotice(t, s, ctx, c, scheduledFor, deliverBy)

	sender := &spySender{}
	r := outbox.New(s, sender, nil)
	result, err := r.ProcessOne(ctx, entry)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if result != outbox.ResultDelivered {
		t.Fatalf("result: got %v want %v", result, outbox.ResultDelivered)
	}
	if !sender.called {
		t.Fatal("sender should have been called")
	}

	delivered, err := s.NoticeDelivered(ctx, a.AttemptID, scheduledFor)
	if err != nil {
		t.Fatalf("notice check: %v", err)
	}
	if !delivered {
		t.Fatal("notice should now read as delivered and within the 24h window")
	}
}

func TestD2ProcessOneCancelsWithoutSendingWhenPastDeadline(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	scheduledFor := time.Now().UTC().Add(1 * time.Hour)
	deliverBy := time.Now().UTC().Add(-1 * time.Minute) // deadline already passed
	a, entry := seedAttemptWithNotice(t, s, ctx, c, scheduledFor, deliverBy)

	sender := &spySender{}
	r := outbox.New(s, sender, nil)
	result, err := r.ProcessOne(ctx, entry)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if result != outbox.ResultTooLate {
		t.Fatalf("result: got %v want %v", result, outbox.ResultTooLate)
	}
	if sender.called {
		t.Fatal("must not send a notice that can no longer satisfy the 24h requirement")
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeAbandonedStale {
		t.Fatalf("outcome: got %v want ABANDONED_STALE", got.Outcome)
	}
	cycle, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if cycle.AttemptsUsed != c.AttemptsUsed-1 {
		t.Fatalf("attemptsUsed: got %d want %d (budget must be refunded immediately, not 23 hours from now)",
			cycle.AttemptsUsed, c.AttemptsUsed-1)
	}
}

func TestProcessOneRetriesOnSendFailureWithoutAbandoning(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	scheduledFor := time.Now().UTC().Add(30 * time.Hour)
	deliverBy := scheduledFor.Add(-24 * time.Hour)
	a, entry := seedAttemptWithNotice(t, s, ctx, c, scheduledFor, deliverBy)

	sender := &spySender{err: errors.New("transient network error")}
	r := outbox.New(s, sender, nil)
	result, err := r.ProcessOne(ctx, entry)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if result != outbox.ResultSendFailed {
		t.Fatalf("result: got %v want %v", result, outbox.ResultSendFailed)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Outcome != nil {
		t.Fatalf("a send failure with time still on the clock must not abandon anything, got outcome=%v", *got.Outcome)
	}
	cycle, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if cycle.AttemptsUsed != c.AttemptsUsed {
		t.Fatalf("attemptsUsed: got %d want %d (still has time — no refund yet)", cycle.AttemptsUsed, c.AttemptsUsed)
	}
}

func TestPendingNoticesExcludesAlreadyAbandonedEntries(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	scheduledFor := time.Now().UTC().Add(1 * time.Hour)
	deliverBy := time.Now().UTC().Add(-1 * time.Minute) // deadline already passed
	_, entry := seedAttemptWithNotice(t, s, ctx, c, scheduledFor, deliverBy)

	sender := &spySender{}
	r := outbox.New(s, sender, nil)
	if result, err := r.ProcessOne(ctx, entry); err != nil {
		t.Fatalf("first process: %v", err)
	} else if result != outbox.ResultTooLate {
		t.Fatalf("first process result: got %v want %v", result, outbox.ResultTooLate)
	}

	pending, err := s.PendingNotices(ctx, 50)
	if err != nil {
		t.Fatalf("pending notices: %v", err)
	}
	for _, p := range pending {
		if p.ID == entry.ID {
			t.Fatal("a too-late notice must stop showing up as pending once its attempt is abandoned — " +
				"otherwise the relay re-processes and re-logs it on every tick, forever")
		}
	}
}

func TestProcessBatchHandlesMultiplePending(t *testing.T) {
	s, ctx := testStore(t)
	c1 := seedCycle(t, s, ctx)
	c2 := seedCycle(t, s, ctx)
	scheduledFor := time.Now().UTC().Add(30 * time.Hour)
	onTime := scheduledFor.Add(-24 * time.Hour)
	late := time.Now().UTC().Add(-1 * time.Minute)

	seedAttemptWithNotice(t, s, ctx, c1, scheduledFor, onTime)
	seedAttemptWithNotice(t, s, ctx, c2, scheduledFor, late)

	sender := &spySender{}
	r := outbox.New(s, sender, nil)
	results, err := r.ProcessBatch(ctx, 10)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	delivered, tooLate := 0, 0
	for _, r := range results {
		switch r {
		case outbox.ResultDelivered:
			delivered++
		case outbox.ResultTooLate:
			tooLate++
		}
	}
	if delivered < 1 || tooLate < 1 {
		t.Fatalf("expected both outcomes represented, got delivered=%d tooLate=%d", delivered, tooLate)
	}
}
