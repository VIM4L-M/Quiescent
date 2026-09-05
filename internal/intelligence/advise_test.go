package intelligence_test

import (
	"context"
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/intelligence"
)

func TestAdviseFallsBackWithoutAKey(t *testing.T) {
	c := intelligence.New("", "openai/gpt-oss-120b")
	proposal, err := c.Advise(context.Background(), domain.FailureCodeStat{
		Code:        "SOME_UNMAPPED_CODE",
		Occurrences: 12,
		Recovered:   3,
		Terminal:    9,
	})
	if err != nil {
		t.Fatalf("advise must never return an error, got %v", err)
	}
	if proposal.Class != domain.ClassManualReview {
		t.Fatalf("fallback proposal must be manual_review, got %v", proposal.Class)
	}
	if proposal.Rationale == "" {
		t.Fatal("expected a non-empty fallback rationale")
	}
}
