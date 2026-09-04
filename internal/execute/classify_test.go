package execute

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

func newUUIDWB(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func testStoreWB(t *testing.T) (*store.Store, context.Context) {
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

func seedFreshCycleWB(t *testing.T, s *store.Store, ctx context.Context) domain.MandateCycle {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUIDWB(t)),
		MandateID:    domain.MandateID(newUUIDWB(t)),
		CustomerID:   domain.CustomerID(newUUIDWB(t)),
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

type fakeClassifier struct {
	class      domain.FailureClass
	confidence float64
	err        error
}

func (f fakeClassifier) Propose(_ context.Context, _ domain.FailureCode, _ domain.ClassificationContext) (domain.Proposal, error) {
	return domain.Proposal{Class: f.class, Confidence: f.confidence, Rationale: "fake"}, f.err
}

func TestClassifyUsesMappedCodeWithoutConsultingAI(t *testing.T) {
	s, ctx := testStoreWB(t)
	c := seedFreshCycleWB(t, s, ctx)
	w := &Worker{Store: s, Log: slog.Default(), Classifier: fakeClassifier{class: domain.ClassTerminal, confidence: 1}}

	got := w.classify(ctx, c, domain.FailureInsufficientFunds)
	if got != domain.ClassRetryLater {
		t.Fatalf("mapped code must use the table, not the AI: got %v want %v", got, domain.ClassRetryLater)
	}
}

func TestClassifyUsesAIProposalAboveThreshold(t *testing.T) {
	s, ctx := testStoreWB(t)
	c := seedFreshCycleWB(t, s, ctx)
	w := &Worker{Store: s, Log: slog.Default(), Classifier: fakeClassifier{class: domain.ClassTerminal, confidence: 0.9}}

	got := w.classify(ctx, c, "SOME_UNMAPPED_CODE")
	if got != domain.ClassTerminal {
		t.Fatalf("got %v want %v (the AI's confident proposal)", got, domain.ClassTerminal)
	}
}

func TestClassifyIgnoresLowConfidenceAIProposal(t *testing.T) {
	s, ctx := testStoreWB(t)
	c := seedFreshCycleWB(t, s, ctx)
	w := &Worker{Store: s, Log: slog.Default(), Classifier: fakeClassifier{class: domain.ClassRetryNow, confidence: 0.3}}

	got := w.classify(ctx, c, "SOME_UNMAPPED_CODE")
	if got != domain.ClassManualReview {
		t.Fatalf("a low-confidence AI proposal must be overridden to manual_review, got %v", got)
	}
}

func TestClassifyFallsBackToManualReviewWithNoClassifierConfigured(t *testing.T) {
	s, ctx := testStoreWB(t)
	c := seedFreshCycleWB(t, s, ctx)
	w := &Worker{Store: s, Log: slog.Default()}

	got := w.classify(ctx, c, "SOME_UNMAPPED_CODE")
	if got != domain.ClassManualReview {
		t.Fatalf("with no AI configured, an unmapped code must still fall back to manual_review, got %v", got)
	}
}

func TestClassifyFallsBackToManualReviewOnAIError(t *testing.T) {
	s, ctx := testStoreWB(t)
	c := seedFreshCycleWB(t, s, ctx)
	w := &Worker{Store: s, Log: slog.Default(), Classifier: fakeClassifier{err: fmt.Errorf("boom")}}

	got := w.classify(ctx, c, "SOME_UNMAPPED_CODE")
	if got != domain.ClassManualReview {
		t.Fatalf("an AI error must fall back to manual_review, got %v", got)
	}
}
