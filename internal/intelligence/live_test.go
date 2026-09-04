package intelligence_test

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/intelligence"
)

var fallbackShape = regexp.MustCompile(`^\d+ attempt\(s\) on record\. Most recent: attempt \d+, status \S+\.$`)

func TestNarrateHitsTheRealAPIWhenAKeyIsPresent(t *testing.T) {
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		t.Skip("GROQ_API_KEY not set")
	}

	c := intelligence.New(key, "openai/gpt-oss-120b")

	insufficient := domain.FailureInsufficientFunds
	failure := domain.OutcomeFailure
	success := domain.OutcomeSuccess

	attempts := []domain.Attempt{
		{
			AttemptID: "a1", CycleID: "c1", Seq: 1, IdempotencyKey: "c1:1",
			Outcome: &failure, FailureCode: &insufficient,
			DecisionReason: domain.DecisionReason{Class: domain.ClassRetryLater, PredictionBasis: "low balance on record"},
		},
		{
			AttemptID: "a2", CycleID: "c1", Seq: 2, IdempotencyKey: "c1:2",
			Outcome:        &success,
			DecisionReason: domain.DecisionReason{Class: domain.ClassRetryLater, PredictionBasis: "shifted past due-date balance window"},
		},
	}

	text, err := c.Narrate(context.Background(), attempts)
	if err != nil {
		t.Fatalf("narrate: %v", err)
	}
	if fallbackShape.MatchString(text) {
		t.Fatalf("got the deterministic fallback template, not a real model response — "+
			"the API call failed silently: %q", text)
	}
	t.Logf("real narration: %s", text)
}
