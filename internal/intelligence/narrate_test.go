package intelligence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/intelligence"
)

func attempt(seq int16, outcome *domain.Outcome) domain.Attempt {
	return domain.Attempt{
		AttemptID:      domain.AttemptID("a"),
		CycleID:        domain.CycleID("c"),
		Seq:            seq,
		IdempotencyKey: "c:1",
		ScheduledFor:   time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		Outcome:        outcome,
		DecisionReason: domain.DecisionReason{
			Class:           domain.ClassRetryLater,
			PredictionBasis: "test",
		},
	}
}

func TestNarrateFallsBackWithoutAKey(t *testing.T) {
	c := intelligence.New("", "claude-opus-5")
	outcome := domain.OutcomeFailure
	text, err := c.Narrate(context.Background(), []domain.Attempt{attempt(1, &outcome)})
	if err != nil {
		t.Fatalf("narrate must never return an error, got %v", err)
	}
	if text == "" {
		t.Fatal("expected a non-empty fallback narrative")
	}
}

func TestNarrateRejectsEmptyAttempts(t *testing.T) {
	c := intelligence.New("", "claude-opus-5")
	_, err := c.Narrate(context.Background(), nil)
	if !errors.Is(err, intelligence.ErrNoAttempts) {
		t.Fatalf("want ErrNoAttempts, got %v", err)
	}
}

func TestNarrateNeverBlocksPastItsTimeout(t *testing.T) {
	c := intelligence.New("bad-key-forces-a-real-call-that-will-fail-fast", "claude-opus-5")
	start := time.Now()
	outcome := domain.OutcomeFailure
	if _, err := c.Narrate(context.Background(), []domain.Attempt{attempt(1, &outcome)}); err != nil {
		t.Fatalf("narrate must never return an error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("narrate took %s — the bounded timeout did not bound it", elapsed)
	}
}
