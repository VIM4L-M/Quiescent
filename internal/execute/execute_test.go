package execute_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/execute"
	"github.com/VIM4L-M/Quiescent/internal/lease"
	"github.com/VIM4L-M/Quiescent/internal/provider"
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

func testBank(t *testing.T) (*provider.Client, string) {
	t.Helper()
	sim, err := provider.New(provider.Config{
		Seed:       42,
		LedgerPath: filepath.Join(t.TempDir(), "ledger.jsonl"),
	}, nil)
	if err != nil {
		t.Fatalf("provider.New: %v", err)
	}
	sim.Now = func() time.Time { return time.Date(2026, 3, 5, 8, 0, 0, 0, time.UTC) }
	srv := httptest.NewServer(sim.Handler())
	t.Cleanup(func() {
		srv.Close()
		sim.Close()
	})
	return provider.NewClient(srv.URL, 5*time.Second), srv.URL
}

func inject(t *testing.T, baseURL string, req provider.InjectRequest) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal inject: %v", err)
	}
	resp, err := http.Post(baseURL+"/inject", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inject: status %d", resp.StatusCode)
	}
}

func seedCycle(t *testing.T, s *store.Store, ctx context.Context, amountPaise int64) domain.MandateCycle {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   domain.CustomerID(newUUID(t)),
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  amountPaise,
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

func seedAttempt(t *testing.T, s *store.Store, ctx context.Context, c domain.MandateCycle, scheduledFor time.Time) domain.Attempt {
	t.Helper()
	a := domain.Attempt{
		AttemptID:      domain.AttemptID(newUUID(t)),
		CycleID:        c.CycleID,
		Seq:            1,
		IdempotencyKey: fmt.Sprintf("%s:1", c.CycleID),
		ScheduledFor:   scheduledFor,
		DecisionReason: reason(),
	}
	if err := s.InsertAttempt(ctx, a); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	return a
}

func deliverNotice(t *testing.T, s *store.Store, ctx context.Context, c domain.MandateCycle, a domain.Attempt) {
	t.Helper()
	if err := s.QueueNotice(ctx, c.CycleID, a.AttemptID, domain.OutboxPreDebitNotice,
		json.RawMessage(`{"amountPaise":`+fmt.Sprint(c.AmountPaise)+`}`), a.ScheduledFor.Add(-24*time.Hour)); err != nil {
		t.Fatalf("queue notice: %v", err)
	}
	if err := s.MarkNoticeDelivered(ctx, a.AttemptID, domain.OutboxPreDebitNotice); err != nil {
		t.Fatalf("deliver notice: %v", err)
	}
}

func TestFireOneRecordsSuccessOrFailureFromBank(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c, time.Now().UTC().Add(25*time.Hour))
	deliverNotice(t, s, ctx, c, a)

	w := execute.New(s, bank, "worker-a", nil)
	result, err := w.FireOne(ctx, a)
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if result != execute.ResultFired {
		t.Fatalf("result: got %v want %v", result, execute.ResultFired)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if !got.Fired() || !got.Resolved() {
		t.Fatalf("attempt should be fired and resolved, got %+v", got)
	}
	if *got.Outcome != domain.OutcomeSuccess && *got.Outcome != domain.OutcomeFailure {
		t.Fatalf("outcome: got %v want SUCCESS or FAILURE", *got.Outcome)
	}
	if *got.Outcome == domain.OutcomeFailure && got.FailureCode == nil {
		t.Fatal("FAILURE outcome must carry a failure code")
	}

	entries, err := s.AuditByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("audit by cycle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(entries))
	}
	if entries[0].Event != "attempt_fired" {
		t.Fatalf("event: got %q want %q", entries[0].Event, "attempt_fired")
	}
	if string(entries[0].CorrelationID) != string(a.AttemptID) {
		t.Fatalf("correlationID: got %q want the attemptID %q", entries[0].CorrelationID, a.AttemptID)
	}
}

func TestFireOneRecordsDeterministicOverLimitFailure(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 2_000_000) // over the UPI cap of 1,500,000
	a := seedAttempt(t, s, ctx, c, time.Now().UTC().Add(25*time.Hour))
	deliverNotice(t, s, ctx, c, a)

	w := execute.New(s, bank, "worker-a", nil)
	if _, err := w.FireOne(ctx, a); err != nil {
		t.Fatalf("fire: %v", err)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeFailure {
		t.Fatalf("outcome: got %v want FAILURE", got.Outcome)
	}
	if got.FailureCode == nil || *got.FailureCode != domain.FailureAmountExceedsLimit {
		t.Fatalf("failureCode: got %v want %v", got.FailureCode, domain.FailureAmountExceedsLimit)
	}
}

func TestC3FireOneMarksTimeoutWhenBankNeverReplies(t *testing.T) {
	s, ctx := testStore(t)
	bank, baseURL := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c, time.Now().UTC().Add(25*time.Hour))
	deliverNotice(t, s, ctx, c, a)

	inject(t, baseURL, provider.InjectRequest{Mode: provider.InjectTimeoutAfterCommit, CycleID: c.CycleID})

	w := execute.New(s, bank, "worker-a", nil)
	result, err := w.FireOne(ctx, a)
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if result != execute.ResultFired {
		t.Fatalf("result: got %v want %v", result, execute.ResultFired)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if !got.Fired() {
		t.Fatal("attempt must still be marked fired — the write-ahead row is what we knew before sending")
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeTimeout {
		t.Fatalf("outcome: got %v want TIMEOUT — the worker must never guess", got.Outcome)
	}

	status, found, err := bank.Status(ctx, a.IdempotencyKey)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !found {
		t.Fatal("the bank should have committed a real decision even though the worker never heard back")
	}
	t.Logf("worker recorded TIMEOUT; bank actually decided %s — exactly the gap reconciliation exists to close", status.Outcome)
}

func TestFireOneSkipsWhenLeaseHeld(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c, time.Now().UTC())
	deliverNotice(t, s, ctx, c, a)

	if _, err := lease.Acquire(ctx, s, c.CycleID, "worker-other", 30*time.Second); err != nil {
		t.Fatalf("pre-acquire lease: %v", err)
	}

	w := execute.New(s, bank, "worker-a", nil)
	result, err := w.FireOne(ctx, a)
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if result != execute.ResultNotMyTurn {
		t.Fatalf("result: got %v want %v", result, execute.ResultNotMyTurn)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Fired() {
		t.Fatal("attempt must not be fired when the lease belongs to someone else")
	}
}

func TestFireOneAbandonsUndeliveredNotice(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c, time.Now().UTC())

	w := execute.New(s, bank, "worker-a", nil)
	result, err := w.FireOne(ctx, a)
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if result != execute.ResultNoticeMissing {
		t.Fatalf("result: got %v want %v", result, execute.ResultNoticeMissing)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Fired() {
		t.Fatal("attempt must not be fired without a delivered notice")
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeAbandonedStale {
		t.Fatalf("outcome: got %v want ABANDONED_STALE", got.Outcome)
	}

	cycle, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if cycle.AttemptsUsed != c.AttemptsUsed-1 {
		t.Fatalf("attemptsUsed: got %d want %d (budget must be refunded)", cycle.AttemptsUsed, c.AttemptsUsed-1)
	}
}

func TestFireOneAbandonsStaleAttempt(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c, time.Now().UTC().Add(-1*time.Hour))
	deliverNotice(t, s, ctx, c, a)

	w := execute.New(s, bank, "worker-a", nil)
	result, err := w.FireOne(ctx, a)
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if result != execute.ResultStale {
		t.Fatalf("result: got %v want %v", result, execute.ResultStale)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Fired() {
		t.Fatal("a stale attempt must never be fired")
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeAbandonedStale {
		t.Fatalf("outcome: got %v want ABANDONED_STALE", got.Outcome)
	}
}
