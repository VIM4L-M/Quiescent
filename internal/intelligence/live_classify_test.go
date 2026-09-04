package intelligence_test

import (
	"context"
	"os"
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/intelligence"
)

func TestProposeHitsTheRealAPIWhenAKeyIsPresent(t *testing.T) {
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		t.Skip("GROQ_API_KEY not set")
	}

	c := intelligence.New(key, "openai/gpt-oss-120b")
	proposal, err := c.Propose(context.Background(), "WEIRD_UNMAPPED_BANK_CODE_XYZ", domain.ClassificationContext{
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  50_000,
		AttemptsUsed: 1,
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if !proposal.Class.Valid() {
		t.Fatalf("class: got %q, not a valid domain.FailureClass", proposal.Class)
	}
	if proposal.Confidence < 0 || proposal.Confidence > 1 {
		t.Fatalf("confidence: got %v, want a value in [0,1]", proposal.Confidence)
	}
	if proposal.Rationale == "" {
		t.Fatal("expected a non-empty rationale from a real model response")
	}
	t.Logf("real proposal: class=%s confidence=%.2f rationale=%s", proposal.Class, proposal.Confidence, proposal.Rationale)
}
