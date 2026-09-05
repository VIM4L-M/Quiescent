package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

const (
	testCycle  domain.CycleID = "3f1b2c4d-0000-4000-8000-000000000001"
	otherCycle domain.CycleID = "3f1b2c4d-0000-4000-8000-000000000002"
)

func newSim(t *testing.T, seed int64, ledger string) (*Sim, *httptest.Server) {
	t.Helper()
	s, err := New(Config{Seed: seed, LedgerPath: ledger},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		srv.Close()
		s.Close()
	})
	return s, srv
}

func at(t *testing.T, iso string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		t.Fatalf("parse %s: %v", iso, err)
	}
	return ts
}

func debit(t *testing.T, c *Client, req DebitRequest) (DebitResponse, error) {
	t.Helper()
	return c.Debit(context.Background(), req)
}

func request(cycle domain.CycleID, seq int, fence, amount int64) DebitRequest {
	return DebitRequest{
		CycleID:        cycle,
		IdempotencyKey: IdempotencyKey(cycle, seq),
		Fence:          fence,
		AmountPaise:    amount,
		Rail:           domain.RailUPIAutopay,
	}
}

func TestDrawIsPureInSeedCycleAndAttempt(t *testing.T) {
	a := Draw(7, testCycle, 2)
	b := Draw(7, testCycle, 2)
	if a != b {
		t.Fatalf("draw not stable: %v vs %v", a, b)
	}
	if Draw(7, testCycle, 3) == a {
		t.Fatal("draw did not vary with attemptNumber")
	}
	if Draw(7, otherCycle, 2) == a {
		t.Fatal("draw did not vary with cycleID")
	}
	if Draw(8, testCycle, 2) == a {
		t.Fatal("draw did not vary with seed")
	}
	if a < 0 || a >= 1 {
		t.Fatalf("draw out of range: %v", a)
	}
}

func TestOutcomeIndependentOfCallerAndOrder(t *testing.T) {
	firedAt := at(t, "2026-03-08T09:00:00+05:30")
	base := Conditions{
		Seed:          99,
		CycleID:       testCycle,
		AttemptNumber: 3,
		Rail:          domain.RailUPIAutopay,
		AmountPaise:   49_900,
		BalancePaise:  1_200_000,
		FiredAt:       firedAt,
	}
	want := Decide(base)
	for i := 0; i < 50; i++ {
		Decide(Conditions{Seed: 99, CycleID: otherCycle, AttemptNumber: i,
			Rail: domain.RailENACH, AmountPaise: 1, BalancePaise: 1, FiredAt: firedAt})
	}
	if got := Decide(base); got != want {
		t.Fatalf("outcome drifted with call history: %+v vs %+v", got, want)
	}
}

func TestFenceRejectsStrictlyLowerAndAcceptsEqual(t *testing.T) {
	_, srv := newSim(t, 1, filepath.Join(t.TempDir(), "ledger.jsonl"))
	c := NewClient(srv.URL, 2*time.Second)

	if _, err := debit(t, c, request(testCycle, 1, 8, 49_900)); err != nil {
		t.Fatalf("fence 8: %v", err)
	}
	if _, err := debit(t, c, request(testCycle, 1, 8, 49_900)); err != nil {
		t.Fatalf("same fence retry must be accepted: %v", err)
	}
	_, err := debit(t, c, request(testCycle, 2, 7, 49_900))
	if !errors.Is(err, ErrStaleFence) {
		t.Fatalf("fence 7 after 8 should be stale, got %v", err)
	}
	if _, err := debit(t, c, request(testCycle, 2, 9, 49_900)); err != nil {
		t.Fatalf("fence 9: %v", err)
	}
}

func TestIdempotencyReplaysAndConflicts(t *testing.T) {
	_, srv := newSim(t, 1, filepath.Join(t.TempDir(), "ledger.jsonl"))
	c := NewClient(srv.URL, 2*time.Second)

	first, err := debit(t, c, request(testCycle, 1, 1, 49_900))
	if err != nil {
		t.Fatalf("first debit: %v", err)
	}
	if first.Replayed {
		t.Fatal("first debit reported as replayed")
	}

	again, err := debit(t, c, request(testCycle, 1, 1, 49_900))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed {
		t.Fatal("second debit under the same key was reprocessed")
	}
	if again.Outcome != first.Outcome || !again.DebitedAt.Equal(first.DebitedAt) {
		t.Fatalf("replay differed: %+v vs %+v", again, first)
	}

	_, err = debit(t, c, request(testCycle, 1, 1, 50_000))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed amount under the same key should conflict, got %v", err)
	}
}

func TestTimeoutAfterCommitSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	s, srv := newSim(t, 42, path)
	c := NewClient(srv.URL, 2*time.Second)

	inject(t, s, InjectRequest{Mode: InjectTimeoutAfterCommit, CycleID: testCycle, Count: 1})

	key := IdempotencyKey(testCycle, 1)
	if _, err := debit(t, c, request(testCycle, 1, 1, 49_900)); err == nil {
		t.Fatal("timeoutAfterCommit must not return a response")
	}
	srv.Close()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	restarted, srv2 := newSim(t, 42, path)
	c2 := NewClient(srv2.URL, 2*time.Second)
	rec, found, err := c2.Status(context.Background(), key)
	if err != nil {
		t.Fatalf("status after restart: %v", err)
	}
	if !found {
		t.Fatal("restarted sim forgot a committed debit")
	}
	if rec.IdempotencyKey != key {
		t.Fatalf("wrong record: %+v", rec)
	}
	if got := restarted.ledger.HighestFence(testCycle); got != 1 {
		t.Fatalf("fence high-water lost across restart: %d", got)
	}
}

func TestTimeoutBeforeCommitLeavesNoRecord(t *testing.T) {
	s, srv := newSim(t, 42, filepath.Join(t.TempDir(), "ledger.jsonl"))
	c := NewClient(srv.URL, 2*time.Second)

	inject(t, s, InjectRequest{Mode: InjectTimeoutBeforeCommit, CycleID: testCycle, Count: 1})

	key := IdempotencyKey(testCycle, 1)
	if _, err := debit(t, c, request(testCycle, 1, 1, 49_900)); err == nil {
		t.Fatal("timeoutBeforeCommit must not return a response")
	}
	if _, found, err := c.Status(context.Background(), key); err != nil || found {
		t.Fatalf("money must not have moved: found=%v err=%v", found, err)
	}
	if _, err := debit(t, c, request(testCycle, 1, 1, 49_900)); err != nil {
		t.Fatalf("retry after a pre-commit drop must be accepted: %v", err)
	}
}

func TestBlockedWindowDeclines(t *testing.T) {
	s, srv := newSim(t, 42, filepath.Join(t.TempDir(), "ledger.jsonl"))
	c := NewClient(srv.URL, 2*time.Second)
	s.Now = func() time.Time { return at(t, "2026-03-08T11:00:00+05:30") }

	resp, err := debit(t, c, request(testCycle, 1, 1, 1_000))
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	if resp.Outcome != domain.OutcomeFailure || resp.FailureCode != FailureTechnicalDecline {
		t.Fatalf("blocked window must decline, got %+v", resp)
	}
}

func TestIgnoreBlockedWindowsBypassesTheDeclineForTesting(t *testing.T) {
	s, err := New(Config{Seed: 42, LedgerPath: filepath.Join(t.TempDir(), "ledger.jsonl"), IgnoreBlockedWindows: true},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close(); s.Close() })
	c := NewClient(srv.URL, 2*time.Second)
	s.Now = func() time.Time { return at(t, "2026-03-08T11:00:00+05:30") } // inside the 10:00-13:00 IST window

	resp, err := debit(t, c, request(testCycle, 1, 1, 1_000))
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	if resp.Outcome == domain.OutcomeFailure && resp.FailureCode == FailureTechnicalDecline {
		t.Fatal("IgnoreBlockedWindows must bypass the blocked-window decline")
	}
}

func TestENACHBounceChargeOnInsufficientFunds(t *testing.T) {
	s, srv := newSim(t, 42, filepath.Join(t.TempDir(), "ledger.jsonl"))
	c := NewClient(srv.URL, 2*time.Second)
	s.Now = func() time.Time { return at(t, "2026-03-08T09:00:00+05:30") }
	s.world.Apply(Fixture{
		Customers: []Customer{{CustomerID: "poor", Timeline: []BalancePoint{
			{From: at(t, "2026-01-01T00:00:00+05:30"), BalancePaise: 34_000},
		}}},
		Cycles: []CycleFixture{{CycleID: testCycle, CustomerID: "poor"}},
	})

	req := request(testCycle, 1, 1, 500_000)
	req.Rail = domain.RailENACH
	resp, err := debit(t, c, req)
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	if resp.FailureCode != domain.FailureInsufficientFunds {
		t.Fatalf("expected insufficient funds, got %+v", resp)
	}
	if resp.BouncePaise == 0 {
		t.Fatal("eNACH insufficient funds must levy a bounce charge")
	}
}

func TestRevokeMandateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	s, srv := newSim(t, 42, path)
	inject(t, s, InjectRequest{Mode: InjectRevokeMandate, CycleID: testCycle})
	srv.Close()
	s.Close()

	restarted, srv2 := newSim(t, 42, path)
	if !restarted.ledger.Revoked(testCycle) {
		t.Fatal("restarted sim forgot a revoked mandate")
	}
	c := NewClient(srv2.URL, 2*time.Second)
	restarted.Now = func() time.Time { return at(t, "2026-03-08T09:00:00+05:30") }
	resp, err := debit(t, c, request(testCycle, 1, 1, 1_000))
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	if resp.FailureCode != domain.FailureMandateRevoked {
		t.Fatalf("expected mandate revoked, got %+v", resp)
	}
}

func TestSeedMismatchRefusesToStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	s, srv := newSim(t, 1, path)
	srv.Close()
	s.Close()

	if _, err := New(Config{Seed: 2, LedgerPath: path},
		slog.New(slog.NewTextHandler(io.Discard, nil))); !errors.Is(err, ErrSeedMismatch) {
		t.Fatalf("expected seed mismatch, got %v", err)
	}
}

func TestOracleIsNotOnThePublicMux(t *testing.T) {
	s, srv := newSim(t, 42, filepath.Join(t.TempDir(), "ledger.jsonl"))
	resp, err := http.Get(srv.URL + "/oracle/probabilities?cycleID=" + string(testCycle))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("oracle reachable from the kernel mux: %d", resp.StatusCode)
	}

	oracle := httptest.NewServer(s.OracleHandler())
	defer oracle.Close()
	out, err := NewOracleClient(oracle.URL, 2*time.Second).Probabilities(context.Background(),
		OracleQuery{CycleID: testCycle, Rail: domain.RailUPIAutopay, AmountPaise: 49_900,
			From: at(t, "2026-03-01T00:00:00+05:30"), Days: 1, StepMinutes: 60})
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if len(out.Slots) != 24 {
		t.Fatalf("expected 24 slots, got %d", len(out.Slots))
	}
	blocked := 0
	for _, slot := range out.Slots {
		if slot.Blocked {
			blocked++
			for _, p := range slot.Probabilities {
				if p != 0 {
					t.Fatalf("blocked slot carries a non-zero probability: %v", slot)
				}
			}
		}
	}
	if blocked != 9 {
		t.Fatalf("expected 9 blocked hourly slots (10,11,12,13 and 17,18,19,20,21 IST), got %d", blocked)
	}
}

func inject(t *testing.T, s *Sim, req InjectRequest) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/inject", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inject returned %d", resp.StatusCode)
	}
}

func TestConcurrentIdenticalDebitsCommitOnce(t *testing.T) {
	_, srv := newSim(t, 5, filepath.Join(t.TempDir(), "ledger.jsonl"))
	c := NewClient(srv.URL, 5*time.Second)

	const callers = 16
	var wg sync.WaitGroup
	fresh := make(chan bool, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Debit(context.Background(), request(testCycle, 1, 3, 49_900))
			if err != nil {
				t.Errorf("debit: %v", err)
				return
			}
			fresh <- !resp.Replayed
		}()
	}
	wg.Wait()
	close(fresh)

	committed := 0
	for f := range fresh {
		if f {
			committed++
		}
	}
	if committed != 1 {
		t.Fatalf("expected exactly one committed debit, got %d", committed)
	}
}

func TestConcurrentStaleFenceNeverAdmitted(t *testing.T) {
	s, srv := newSim(t, 5, filepath.Join(t.TempDir(), "ledger.jsonl"))
	c := NewClient(srv.URL, 5*time.Second)

	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := map[int64]bool{}
	for i := 0; i < 32; i++ {
		fence := int64(1 + i%4)
		wg.Add(1)
		go func(seq int, fence int64) {
			defer wg.Done()
			_, err := c.Debit(context.Background(), request(testCycle, seq, fence, 49_900))
			if err != nil && !errors.Is(err, ErrStaleFence) {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if err == nil {
				mu.Lock()
				admitted[fence] = true
				mu.Unlock()
			}
		}(i+1, fence)
	}
	wg.Wait()

	highest := s.ledger.HighestFence(testCycle)
	for fence := range admitted {
		if fence > highest {
			t.Fatalf("admitted fence %d above the recorded high-water %d", fence, highest)
		}
	}
	if !admitted[highest] {
		t.Fatalf("high-water %d was recorded without an admitted request", highest)
	}
}
