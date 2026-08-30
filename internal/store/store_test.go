package store

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

func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		t.Skip("DB_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	_, err = s.pool.Exec(ctx,
		`TRUNCATE audit_log, outbox, attempts, leases, mandate_cycles RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return s, ctx
}

func seedCycle(t *testing.T, s *Store, ctx context.Context) domain.MandateCycle {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   domain.CustomerID(newUUID(t)),
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  200000,
		DueDate:      time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
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
		PredictedFunds:  "2026-03-08",
		PredictionBasis: "4_of_5_cycles_succeeded_day_8",
		Constraints: domain.ReasonConstraints{
			BlockedWindowShift: "10:30 -> 18:00",
			NoticeDeadline:     "2026-03-07T18:00Z",
			RailRules:          "upi_autopay",
		},
		BudgetBefore: 1,
		BudgetAfter:  2,
	}
}

func seedAttempt(t *testing.T, s *Store, ctx context.Context, c domain.MandateCycle, seq int16) domain.Attempt {
	t.Helper()
	a := domain.Attempt{
		AttemptID:      domain.AttemptID(newUUID(t)),
		CycleID:        c.CycleID,
		Seq:            seq,
		IdempotencyKey: fmt.Sprintf("%s:%d", c.CycleID, seq),
		ScheduledFor:   time.Date(2026, 3, 8, 18, 0, 0, 0, time.UTC),
		DecisionReason: reason(),
	}
	if err := s.InsertAttempt(ctx, a); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	return a
}

func codePtr(c domain.FailureCode) *domain.FailureCode { return &c }

func TestCycleRoundTripsThroughNamedStringTypes(t *testing.T) {
	s, ctx := testStore(t)
	want := seedCycle(t, s, ctx)

	got, err := s.Cycle(ctx, want.CycleID)
	if err != nil {
		t.Fatalf("load cycle: %v", err)
	}
	if got.CycleID != want.CycleID || got.MandateID != want.MandateID || got.CustomerID != want.CustomerID {
		t.Fatalf("ids differ: got %+v want %+v", got, want)
	}
	if got.Rail != want.Rail || got.State != want.State {
		t.Fatalf("enums differ: got rail=%q state=%q", got.Rail, got.State)
	}
	if got.AmountPaise != want.AmountPaise || got.AttemptsUsed != want.AttemptsUsed {
		t.Fatalf("numbers differ: got %d paise, %d used", got.AmountPaise, got.AttemptsUsed)
	}
	if !got.DueDate.Equal(want.DueDate) {
		t.Fatalf("dueDate differs: got %s want %s", got.DueDate, want.DueDate)
	}
	if got.Version != 0 {
		t.Fatalf("version should default to 0, got %d", got.Version)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("updatedAt should be set by the database default")
	}
	if got.Disposition() != nil {
		t.Fatalf("pending is not terminal, disposition should be nil, got %v", *got.Disposition())
	}
	if got.BudgetRemaining() != 3 {
		t.Fatalf("budgetRemaining: got %d want 3", got.BudgetRemaining())
	}
}

func TestCreateCycleSeedsLeaseAtEpoch(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	var holder *string
	var fence int64
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT holder, fence, expires_at FROM leases WHERE cycle_id = $1`, c.CycleID).
		Scan(&holder, &fence, &expiresAt)
	if err != nil {
		t.Fatalf("lease row missing: %v", err)
	}
	if holder != nil || fence != 0 {
		t.Fatalf("seeded lease should be unheld at fence 0, got holder=%v fence=%d", holder, fence)
	}
	if expiresAt.Year() != 1970 {
		t.Fatalf("seeded lease should expire at epoch, got %s", expiresAt)
	}
}

func TestCreateCycleRejectsInvalidEnums(t *testing.T) {
	s, ctx := testStore(t)
	base := domain.MandateCycle{
		CycleID:     domain.CycleID(newUUID(t)),
		MandateID:   domain.MandateID(newUUID(t)),
		CustomerID:  domain.CustomerID(newUUID(t)),
		Rail:        domain.RailENACH,
		AmountPaise: 200000,
		DueDate:     time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		State:       domain.StatePending,
	}

	bad := base
	bad.State = "in-flight"
	if err := s.CreateCycle(ctx, bad); !errors.Is(err, ErrInvalidEnum) {
		t.Fatalf("state: want ErrInvalidEnum, got %v", err)
	}
	bad = base
	bad.Rail = "card"
	if err := s.CreateCycle(ctx, bad); !errors.Is(err, ErrInvalidEnum) {
		t.Fatalf("rail: want ErrInvalidEnum, got %v", err)
	}
	bad = base
	bad.AttemptsUsed = 5
	if err := s.CreateCycle(ctx, bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("attemptsUsed: want ErrInvalidArgument, got %v", err)
	}
}

func TestC3WriteAheadRejectsPreResolvedAttempt(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	firedAt := time.Now().UTC()
	outcome := domain.OutcomeSuccess
	fence := int64(7)

	mutations := map[string]func(*domain.Attempt){
		"outcome": func(a *domain.Attempt) { a.Outcome = &outcome },
		"firedAt": func(a *domain.Attempt) { a.FiredAt = &firedAt },
		"fence":   func(a *domain.Attempt) { a.Fence = &fence },
	}
	for name, mutate := range mutations {
		a := domain.Attempt{
			AttemptID:      domain.AttemptID(newUUID(t)),
			CycleID:        c.CycleID,
			Seq:            2,
			IdempotencyKey: fmt.Sprintf("%s:2", c.CycleID),
			ScheduledFor:   time.Date(2026, 3, 8, 18, 0, 0, 0, time.UTC),
			DecisionReason: reason(),
		}
		mutate(&a)
		if err := s.InsertAttempt(ctx, a); !errors.Is(err, ErrWriteAheadViolation) {
			t.Fatalf("%s: want ErrWriteAheadViolation, got %v", name, err)
		}
	}
}

func TestAttemptRoundTripsDecisionReason(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	want := seedAttempt(t, s, ctx, c, 2)

	got, err := s.Attempt(ctx, want.AttemptID)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	if got.Fired() || got.Resolved() || got.Fence != nil {
		t.Fatalf("write-ahead row should be unfired and unresolved, got %+v", got)
	}
	if got.DecisionReason != want.DecisionReason {
		t.Fatalf("decisionReason differs:\n got %+v\nwant %+v", got.DecisionReason, want.DecisionReason)
	}
	if got.DecisionReason.Confidence != nil {
		t.Fatalf("confidence should stay nil for a table classification, got %v", *got.DecisionReason.Confidence)
	}
}

func TestAttemptDuplicateSeqIsRejectedByTheDatabase(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	seedAttempt(t, s, ctx, c, 2)

	dup := domain.Attempt{
		AttemptID:      domain.AttemptID(newUUID(t)),
		CycleID:        c.CycleID,
		Seq:            2,
		IdempotencyKey: fmt.Sprintf("%s:2-other", c.CycleID),
		ScheduledFor:   time.Date(2026, 3, 9, 18, 0, 0, 0, time.UTC),
		DecisionReason: reason(),
	}
	if err := s.InsertAttempt(ctx, dup); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
}

func TestMarkAttemptFiredStampsOnceAndCarriesTheFence(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 2)

	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(7)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	got, err := s.Attempt(ctx, a.AttemptID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.Fired() {
		t.Fatal("firedAt should be stamped before the debit leaves")
	}
	if got.Fence == nil || *got.Fence != 7 {
		t.Fatalf("fence: got %v want 7", got.Fence)
	}
	if got.Resolved() {
		t.Fatal("outcome must still be NULL after firing")
	}
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(8)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second stamp: want ErrConflict, got %v", err)
	}
}

func TestRecordOutcomeRequiresAFiredAttempt(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 2)

	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeSuccess, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("unfired SUCCESS: want ErrConflict, got %v", err)
	}
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(7)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeSuccess, nil); err != nil {
		t.Fatalf("fired SUCCESS: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeFailure,
		codePtr(domain.FailureInsufficientFunds)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second outcome: want ErrConflict, got %v", err)
	}
}

func TestE2AbandonedStaleOnlyOnAnUnfiredAttempt(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	stale := seedAttempt(t, s, ctx, c, 2)
	if err := s.RecordAttemptOutcome(ctx, stale.AttemptID, domain.OutcomeAbandonedStale, nil); err != nil {
		t.Fatalf("unfired ABANDONED_STALE: %v", err)
	}

	fired := seedAttempt(t, s, ctx, c, 3)
	if err := s.MarkAttemptFired(ctx, fired.AttemptID, domain.Fence(9)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, fired.AttemptID, domain.OutcomeAbandonedStale, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("fired ABANDONED_STALE: want ErrConflict, got %v", err)
	}
}

func TestResolveOnlyLiftsATimeout(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	timedOut := seedAttempt(t, s, ctx, c, 2)
	if err := s.MarkAttemptFired(ctx, timedOut.AttemptID, domain.Fence(7)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, timedOut.AttemptID, domain.OutcomeTimeout, nil); err != nil {
		t.Fatalf("record timeout: %v", err)
	}
	if err := s.ResolveAttemptOutcome(ctx, timedOut.AttemptID, domain.OutcomeSuccess, nil); err != nil {
		t.Fatalf("resolve timeout: %v", err)
	}
	got, err := s.Attempt(ctx, timedOut.AttemptID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Outcome == nil || *got.Outcome != domain.OutcomeSuccess {
		t.Fatalf("outcome: got %v want SUCCESS", got.Outcome)
	}

	settled := seedAttempt(t, s, ctx, c, 3)
	if err := s.MarkAttemptFired(ctx, settled.AttemptID, domain.Fence(8)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, settled.AttemptID, domain.OutcomeSuccess, nil); err != nil {
		t.Fatalf("record success: %v", err)
	}
	if err := s.ResolveAttemptOutcome(ctx, settled.AttemptID, domain.OutcomeFailure,
		codePtr(domain.FailureInsufficientFunds)); !errors.Is(err, ErrConflict) {
		t.Fatalf("resolving a SUCCESS must not be possible, got %v", err)
	}
}

func TestOutcomeAndFailureCodeMustAgree(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 2)
	if err := s.MarkAttemptFired(ctx, a.AttemptID, domain.Fence(7)); err != nil {
		t.Fatalf("mark fired: %v", err)
	}

	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeFailure, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("FAILURE without a code: want ErrInvalidArgument, got %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, domain.OutcomeSuccess,
		codePtr(domain.FailureInsufficientFunds)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SUCCESS with a code: want ErrInvalidArgument, got %v", err)
	}
	if err := s.RecordAttemptOutcome(ctx, a.AttemptID, "PARTIAL", nil); !errors.Is(err, ErrInvalidEnum) {
		t.Fatalf("unknown outcome: want ErrInvalidEnum, got %v", err)
	}
}

func TestD2NoticeGateFailsClosedWhenNoticeWasNeverQueued(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 2)

	delivered, err := s.NoticeDelivered(ctx, a.AttemptID, a.ScheduledFor)
	if err != nil {
		t.Fatalf("gate returned an error where it must return false: %v", err)
	}
	if delivered {
		t.Fatal("an attempt with no notice row at all must not be firable")
	}
}

func TestD2NoticeGateOpensOnlyAfterDelivery(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 2)

	_, err := s.pool.Exec(ctx,
		`INSERT INTO outbox (cycle_id, attempt_id, kind, payload, deliver_by)
		 VALUES ($1, $2, $3, $4, $5)`,
		c.CycleID, a.AttemptID, domain.OutboxPreDebitNotice,
		json.RawMessage(`{"amountPaise":200000}`),
		a.ScheduledFor.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("queue notice: %v", err)
	}

	delivered, err := s.NoticeDelivered(ctx, a.AttemptID, a.ScheduledFor)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if delivered {
		t.Fatal("a queued but undelivered notice must not open the gate")
	}

	deliveredAt := a.ScheduledFor.Add(-25 * time.Hour) // comfortably more than 24h ahead
	if _, err := s.pool.Exec(ctx,
		`UPDATE outbox SET delivered_at = $2 WHERE attempt_id = $1`, a.AttemptID, deliveredAt); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	delivered, err = s.NoticeDelivered(ctx, a.AttemptID, a.ScheduledFor)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !delivered {
		t.Fatal("a notice delivered more than 24h ahead of firing must open the gate")
	}

	entries, err := s.OutboxByAttempt(ctx, a.AttemptID, domain.OutboxPreDebitNotice)
	if err != nil {
		t.Fatalf("outbox by attempt: %v", err)
	}
	if len(entries) != 1 || !entries[0].Delivered() || entries[0].Kind != domain.OutboxPreDebitNotice {
		t.Fatalf("outbox round trip: %+v", entries)
	}
}

func TestNoticeGateStaysClosedWhenDeliveredLessThan24hAhead(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 2)

	_, err := s.pool.Exec(ctx,
		`INSERT INTO outbox (cycle_id, attempt_id, kind, payload, deliver_by)
		 VALUES ($1, $2, $3, $4, $5)`,
		c.CycleID, a.AttemptID, domain.OutboxPreDebitNotice,
		json.RawMessage(`{"amountPaise":200000}`),
		a.ScheduledFor.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("queue notice: %v", err)
	}

	// delivered only 23 hours before firing — one hour short of the RBI e-mandate
	// 24h requirement (docs/SOURCES.md, High confidence).
	tooLate := a.ScheduledFor.Add(-23 * time.Hour)
	if _, err := s.pool.Exec(ctx,
		`UPDATE outbox SET delivered_at = $2 WHERE attempt_id = $1`, a.AttemptID, tooLate); err != nil {
		t.Fatalf("deliver late: %v", err)
	}

	delivered, err := s.NoticeDelivered(ctx, a.AttemptID, a.ScheduledFor)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if delivered {
		t.Fatal("a notice delivered less than 24h ahead of firing must NOT open the gate, even though it was delivered")
	}
}

func TestNoticeGateIgnoresOtherKinds(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)
	a := seedAttempt(t, s, ctx, c, 2)

	_, err := s.pool.Exec(ctx,
		`INSERT INTO outbox (cycle_id, attempt_id, kind, payload, deliver_by, delivered_at)
		 VALUES ($1, $2, $3, $4, now(), now())`,
		c.CycleID, a.AttemptID, domain.OutboxEscalation, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("queue escalation: %v", err)
	}

	delivered, err := s.NoticeDelivered(ctx, a.AttemptID, a.ScheduledFor)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if delivered {
		t.Fatal("a delivered escalation is not a pre-debit notice and must not open the gate")
	}
}

func TestAppendAuditRoundTrips(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	e := domain.AuditEntry{
		CycleID:       c.CycleID,
		CorrelationID: domain.CorrelationID(newUUID(t)),
		Event:         "attempt.scheduled",
		Inputs:        json.RawMessage(`{"failureCode":"INSUFFICIENT_FUNDS"}`),
		Decision:      json.RawMessage(`{"scheduledFor":"2026-03-08T18:00:00Z"}`),
		Reason:        "predicted funds on the 8th",
	}
	if err := s.AppendAudit(ctx, e); err != nil {
		t.Fatalf("append audit: %v", err)
	}

	var id int64
	var at time.Time
	var event, reasonText string
	err := s.pool.QueryRow(ctx,
		`SELECT id, at, event, reason FROM audit_log WHERE cycle_id = $1`, c.CycleID).
		Scan(&id, &at, &event, &reasonText)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if id == 0 || at.IsZero() || event != e.Event || reasonText != e.Reason {
		t.Fatalf("audit row: id=%d at=%s event=%q reason=%q", id, at, event, reasonText)
	}

	e.Inputs = nil
	if err := s.AppendAudit(ctx, e); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil inputs: want ErrInvalidArgument, got %v", err)
	}
}

func TestTxRollsBackEverythingOnError(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	sentinel := errors.New("abort")
	err := s.Tx(ctx, func(tx *Store) error {
		if err := tx.InsertAttempt(ctx, domain.Attempt{
			AttemptID:      domain.AttemptID(newUUID(t)),
			CycleID:        c.CycleID,
			Seq:            2,
			IdempotencyKey: fmt.Sprintf("%s:2", c.CycleID),
			ScheduledFor:   time.Date(2026, 3, 8, 18, 0, 0, 0, time.UTC),
			DecisionReason: reason(),
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}

	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		t.Fatalf("attempts by cycle: %v", err)
	}
	if len(attempts) != 0 {
		t.Fatalf("rollback left %d attempts behind", len(attempts))
	}
}

func TestCyclesByStateAndNotFound(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	cycles, err := s.CyclesByState(ctx, domain.StatePending, 10)
	if err != nil {
		t.Fatalf("cycles by state: %v", err)
	}
	if len(cycles) != 1 || cycles[0].CycleID != c.CycleID {
		t.Fatalf("got %d cycles", len(cycles))
	}
	if _, err := s.CyclesByState(ctx, domain.StateInFlight, 10); err != nil {
		t.Fatalf("empty result should not error: %v", err)
	}
	if _, err := s.Cycle(ctx, domain.CycleID(newUUID(t))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
