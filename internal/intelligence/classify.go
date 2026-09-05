package intelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

const ConfidenceThreshold = 0.7

var classifyJSONSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"class": map[string]any{
			"type": "string",
			"enum": []string{"retry_now", "retry_later", "terminal", "manual_review"},
		},
		"confidence": map[string]any{"type": "number"},
		"rationale":  map[string]any{"type": "string"},
	},
	"required":             []string{"class", "confidence", "rationale"},
	"additionalProperties": false,
}

type classifyResult struct {
	Class      string  `json:"class"`
	Confidence float64 `json:"confidence"`
	Rationale  string  `json:"rationale"`
}

func (c *Client) Propose(ctx context.Context, code domain.FailureCode, data domain.ClassificationContext) (domain.Proposal, error) {
	return c.classifyCall(ctx, buildClassifyPrompt(code, data))
}

type Advisor interface {
	Advise(ctx context.Context, stat domain.FailureCodeStat) (domain.Proposal, error)
}

func (c *Client) Advise(ctx context.Context, stat domain.FailureCodeStat) (domain.Proposal, error) {
	return c.classifyCall(ctx, buildAdvisePrompt(stat))
}

func (c *Client) classifyCall(ctx context.Context, prompt string) (domain.Proposal, error) {
	fallback := domain.Proposal{Class: domain.ClassManualReview, Confidence: 0, Rationale: "unavailable, sent to human queue"}

	if c.breaker.open() {
		return fallback, nil
	}
	release, ok := c.acquire()
	if !ok {
		return fallback, nil
	}
	defer release()

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	text, err := c.chatCompletion(callCtx, chatRequest{
		Model:               c.model,
		Messages:            []chatMessage{{Role: "user", Content: prompt}},
		MaxCompletionTokens: 300,
		ResponseFormat: &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchemaSpec{
				Name:   "failure_classification",
				Strict: true,
				Schema: classifyJSONSchema,
			},
		},
	})
	if err != nil {
		c.breaker.recordFailure()
		return fallback, nil
	}

	var result classifyResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		c.breaker.recordFailure()
		return fallback, nil
	}
	c.breaker.recordSuccess()

	class := domain.FailureClass(result.Class)
	if !class.Valid() {
		return fallback, nil
	}
	return domain.Proposal{Class: class, Confidence: result.Confidence, Rationale: result.Rationale}, nil
}

func buildClassifyPrompt(code domain.FailureCode, data domain.ClassificationContext) string {
	var b strings.Builder
	b.WriteString("Classify this payment failure code into exactly one of: " +
		"retry_now (transient, safe to retry immediately once allowed), " +
		"retry_later (likely a funds problem, wait before retrying), " +
		"terminal (retrying can never succeed, e.g. mandate cancelled), " +
		"manual_review (unclear, a human should decide).\n\n")
	fmt.Fprintf(&b, "Failure code: %s\n", code)
	fmt.Fprintf(&b, "Rail: %s\n", data.Rail)
	fmt.Fprintf(&b, "Amount (paise): %d\n", data.AmountPaise)
	fmt.Fprintf(&b, "Attempts used so far: %d\n", data.AttemptsUsed)
	if len(data.PriorCodes) > 0 {
		fmt.Fprintf(&b, "Prior failure codes on this mandate: %v\n", data.PriorCodes)
	}
	if len(data.PriorOutcomes) > 0 {
		fmt.Fprintf(&b, "Prior outcomes on this mandate: %v\n", data.PriorOutcomes)
	}
	b.WriteString("Give a confidence score between 0 and 1, and a one-sentence rationale.")
	return b.String()
}

func buildAdvisePrompt(stat domain.FailureCodeStat) string {
	var b strings.Builder
	b.WriteString("A payment failure code has appeared across many mandate cycles and has no " +
		"permanent classification rule yet. Based on the aggregate outcome below, propose " +
		"whether it should become a permanent rule, classified as exactly one of: " +
		"retry_now, retry_later, terminal, or manual_review. This proposal will be reviewed " +
		"by a human before anything changes — you are not applying it.\n\n")
	fmt.Fprintf(&b, "Failure code: %s\n", stat.Code)
	fmt.Fprintf(&b, "Cycles affected: %d\n", stat.Occurrences)
	fmt.Fprintf(&b, "Of those, eventually recovered: %d\n", stat.Recovered)
	fmt.Fprintf(&b, "Of those, ended terminal (escalated or abandoned): %d\n", stat.Terminal)
	b.WriteString("Give a confidence score between 0 and 1, and a one-sentence rationale " +
		"referencing these numbers.")
	return b.String()
}
