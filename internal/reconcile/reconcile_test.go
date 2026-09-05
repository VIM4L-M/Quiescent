package reconcile_test

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
	"github.com/VIM4L-M/Quiescent/internal/lease"
	"github.com/VIM4L-M/Quiescent/internal/provider"
	"github.com/VIM4L-M/Quiescent/internal/reconcile"
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

func seedAttempt(t *testing.T, s *store.Store, ctx context.Context, c domain.MandateCycle) domain.Attempt {
	t.Helper()
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
	return a
}

// fireAndTimeout replays exactly what execute.Worker does — acquire the lease,
// write-ahead, call the bank — but always ends by marking the attempt TIMEOUT,
// as if the worker's process had crashed or lost the connection right after
// sending. The lease is taken with a 1ms TTL so it has already expired by the
// time the caller (reconcile.Resolve) tries to acquire it.
func fireAndTimeout(t *testing.T, s *store.Store, bank *provider.Client, c domain.MandateCycle, a domain.Attempt) {
	t.Helper()
	ctx := context.Background()

	handle, err := lease.Acquire(ctx, s, c.CycleID, "worker-a", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if err := s.MarkAttemptFired(ctx, a.AttemptID, handle.Fence); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	_, _ = bank.Debit(ctx, provider.DebitRequest{
		CycleID:        c.CycleID,
		IdempotencyKey: a.IdempotencyKey,
		Fence:          int64(handle.Fence),
		AmountPaise:    c.AmountPaise,
		Rail:           c.Rail,
	})
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeTimeout, nil); err != nil {
		t.Fatalf("record timeout: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
}

// fireAndCrash is fireAndTimeout without the final RecordAttemptOutcome call —
// it stops exactly where a real process crash would: fired_at is set, the bank
// has been asked, and nothing ever runs the line that would have recorded
// TIMEOUT. This is the genuine C3 shape, not the in-process-error shape.
func fireAndCrash(t *testing.T, s *store.Store, bank *provider.Client, c domain.MandateCycle, a domain.Attempt) {
	t.Helper()
	ctx := context.Background()

	handle, err := lease.Acquire(ctx, s, c.CycleID, "worker-a", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if err := s.MarkAttemptFired(ctx, a.AttemptID, handle.Fence); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	_, _ = bank.Debit(ctx, provider.DebitRequest{
		CycleID:        c.CycleID,
		IdempotencyKey: a.IdempotencyKey,
		Fence:          int64(handle.Fence),
		AmountPaise:    c.AmountPaise,
		Rail:           c.Rail,
	})
	time.Sleep(300 * time.Millisecond)
}

func TestResolveRecoversWhenDebited(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c)
	fireAndTimeout(t, s, bank, c, a)

	r := reconcile.New(s, bank, "reconciler-a", nil)
	result, err := r.Resolve(ctx, a)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	status, found, err := bank.Status(ctx, a.IdempotencyKey)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !found {
		t.Fatal("bank should have a decision")
	}
	if status.Outcome != domain.OutcomeSuccess {
		t.Skipf("this run's random draw declined the debit (%s); rerun to hit the SUCCESS branch", status.Outcome)
	}

	if result != reconcile.ResultRecovered {
		t.Fatalf("result: got %v want %v", result, reconcile.ResultRecovered)
	}
	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeSuccess {
		t.Fatalf("attempt outcome: got %v want SUCCESS", got.Outcome)
	}
	cycle, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if cycle.State != domain.StateRecovered {
		t.Fatalf("cycle state: got %v want recovered", cycle.State)
	}
}

func TestResolveReturnsToPendingWhenNotDebited(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 2_000_000) // over the UPI cap: deterministic FAILURE
	a := seedAttempt(t, s, ctx, c)
	fireAndTimeout(t, s, bank, c, a)

	r := reconcile.New(s, bank, "reconciler-a", nil)
	result, err := r.Resolve(ctx, a)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result != reconcile.ResultPending {
		t.Fatalf("result: got %v want %v", result, reconcile.ResultPending)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeAbandonedStale {
		t.Fatalf("attempt outcome: got %v want ABANDONED_STALE", got.Outcome)
	}

	cycle, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if cycle.State != domain.StatePending {
		t.Fatalf("cycle state: got %v want pending", cycle.State)
	}
	if cycle.AttemptsUsed != c.AttemptsUsed-1 {
		t.Fatalf("attemptsUsed: got %d want %d (budget must be refunded)", cycle.AttemptsUsed, c.AttemptsUsed-1)
	}

	entries, err := s.AuditByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("audit by cycle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(entries))
	}
	if entries[0].Event != "attempt_reconciled" {
		t.Fatalf("event: got %q want %q", entries[0].Event, "attempt_reconciled")
	}
}

func TestC6ResolveHoldsWhenStillUnknownAfterRetries(t *testing.T) {
	s, ctx := testStore(t)
	bank, baseURL := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c)

	inject(t, baseURL, provider.InjectRequest{Mode: provider.InjectTimeoutBeforeCommit, CycleID: c.CycleID})
	fireAndTimeout(t, s, bank, c, a)

	_, stillFound, err := bank.Status(ctx, a.IdempotencyKey)
	if err != nil {
		t.Fatalf("status precheck: %v", err)
	}
	if stillFound {
		t.Fatal("test setup invalid: bank should never have committed anything")
	}

	r := reconcile.New(s, bank, "reconciler-a", nil)
	r.MaxTries = 2
	r.Backoff = func(int) time.Duration { return time.Millisecond }

	result, err := r.Resolve(ctx, a)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result != reconcile.ResultHeld {
		t.Fatalf("result: got %v want %v", result, reconcile.ResultHeld)
	}

	cycle, err := s.Cycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if cycle.State != domain.StateHeld {
		t.Fatalf("cycle state: got %v want held", cycle.State)
	}

	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeTimeout {
		t.Fatalf("attempt outcome must stay TIMEOUT while held, got %v", got.Outcome)
	}
}

func TestC6ResolveSkipsWhenLeaseHeld(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c)
	fireAndTimeout(t, s, bank, c, a)

	if _, err := lease.Acquire(ctx, s, c.CycleID, "reconciler-other", 5*time.Minute); err != nil {
		t.Fatalf("pre-acquire lease: %v", err)
	}

	r := reconcile.New(s, bank, "reconciler-a", nil)
	result, err := r.Resolve(ctx, a)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result != reconcile.ResultNotMyTurn {
		t.Fatalf("result: got %v want %v — three reconcilers must never race on the same cycle", result, reconcile.ResultNotMyTurn)
	}
}

func TestC3ReconciliationCatchesAGenuineProcessCrashNotJustAnExplicitTimeout(t *testing.T) {
	s, ctx := testStore(t)
	bank, _ := testBank(t)
	c := seedCycle(t, s, ctx, 50_000)
	a := seedAttempt(t, s, ctx, c)
	fireAndCrash(t, s, bank, c, a)

	before, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if before.Outcome != nil {
		t.Fatalf("setup broken: outcome should still be NULL after a simulated crash, got %v", *before.Outcome)
	}

	needing, err := s.NeedsReconciliation(ctx, 50)
	if err != nil {
		t.Fatalf("needs reconciliation: %v", err)
	}
	found := false
	for _, na := range needing {
		if na.AttemptID == a.AttemptID {
			found = true
		}
	}
	if !found {
		t.Fatal("an attempt with fired_at set and outcome truly NULL (a genuine crash) must be found for reconciliation — " +
			"a query that only looks for outcome='TIMEOUT' misses exactly the crash it exists to catch")
	}

	r := reconcile.New(s, bank, "reconciler-a", nil)
	result, err := r.Resolve(ctx, a)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result != reconcile.ResultRecovered && result != reconcile.ResultPending {
		t.Fatalf("result: got %v want recovered or pending", result)
	}

	after, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if after.Outcome == nil {
		t.Fatal("a genuinely crashed attempt must come out of reconciliation resolved, not still NULL forever")
	}
}
