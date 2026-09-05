package verify_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/VIM4L-M/Quiescent/internal/verify"
	"github.com/jackc/pgx/v5/pgxpool"
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

	raw, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("raw pool: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(ctx, `TRUNCATE audit_log, outbox, attempts, leases, mandate_cycles RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
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
		AttemptsUsed: 0,
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
		PredictionBasis: "test fixture",
		Constraints: domain.ReasonConstraints{
			BlockedWindowShift: "n/a",
			NoticeDeadline:     "n/a",
			RailRules:          "upi_autopay",
		},
	}
}

func seedFiredAttempt(t *testing.T, s *store.Store, ctx context.Context, c domain.MandateCycle,
	seq int16, outcome domain.Outcome, code *domain.FailureCode) domain.Attempt {
	t.Helper()
	a := domain.Attempt{
		AttemptID:      domain.AttemptID(newUUID(t)),
		CycleID:        c.CycleID,
		Seq:            seq,
		IdempotencyKey: fmt.Sprintf("%s:%d", c.CycleID, seq),
		ScheduledFor:   time.Now().UTC().Add(-1 * time.Hour),
		DecisionReason: reason(),
	}
	if err := s.InsertAttempt(ctx, a); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(seq)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, outcome, code); err != nil {
		t.Fatalf("record outcome: %v", err)
	}
	return a
}

func findResult(t *testing.T, results []verify.Result, number int) verify.Result {
	t.Helper()
	for _, r := range results {
		if r.Number == number {
			return r
		}
	}
	t.Fatalf("no result for invariant %d", number)
	return verify.Result{}
}

func TestVerifyRunsAllSixInvariants(t *testing.T) {
	s, ctx := testStore(t)
	results, err := verify.Run(ctx, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 invariant results, got %d", len(results))
	}
}

func TestVerifyCatchesADoubleDebit(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	seedFiredAttempt(t, s, ctx, c, 1, domain.OutcomeSuccess, nil)
	seedFiredAttempt(t, s, ctx, c, 2, domain.OutcomeSuccess, nil)

	results, err := verify.Run(ctx, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	inv1 := findResult(t, results, 1)
	if inv1.Passed() {
		t.Fatal("invariant 1 must catch a cycle debited twice")
	}
	found := false
	for _, v := range inv1.Violations {
		if v == string(c.CycleID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected cycle %s among violations, got %v", c.CycleID, inv1.Violations)
	}
	if verify.AllPassed(results) {
		t.Fatal("AllPassed must be false when any invariant has a violation")
	}
}

func TestVerifyCatchesAnAttemptFiredIntoTheBlockedWindow(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	a := domain.Attempt{
		AttemptID:      domain.AttemptID(newUUID(t)),
		CycleID:        c.CycleID,
		Seq:            1,
		IdempotencyKey: fmt.Sprintf("%s:1", c.CycleID),
		ScheduledFor:   time.Now().UTC(),
		DecisionReason: reason(),
	}
	if err := s.InsertAttempt(ctx, a); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(1)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}

	raw, err := pgxpool.New(ctx, os.Getenv("DB_URL"))
	if err != nil {
		t.Fatalf("raw pool: %v", err)
	}
	defer raw.Close()
	fired := time.Date(2026, 3, 8, 11, 0, 0, 0, domain.IST)
	if _, err := raw.Exec(ctx, `UPDATE attempts SET fired_at = $2 WHERE attempt_id = $1`, a.AttemptID, fired); err != nil {
		t.Fatalf("backdate fired_at: %v", err)
	}

	results, err := verify.Run(ctx, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	inv5 := findResult(t, results, 5)
	if inv5.Passed() {
		t.Fatal("invariant 5 must catch an attempt fired at 11:00 IST, inside the 10:00-13:00 blocked window")
	}
}
