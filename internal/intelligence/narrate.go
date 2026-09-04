package intelligence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/anthropics/anthropic-sdk-go"
)

var ErrNoAttempts = errors.New("intelligence: no attempts to narrate")

type Narrator interface {
	Narrate(ctx context.Context, attempts []domain.Attempt) (string, error)
}

func (c *Client) Narrate(ctx context.Context, attempts []domain.Attempt) (string, error) {
	if len(attempts) == 0 {
		return "", ErrNoAttempts
	}
	if c.breaker.open() {
		return fallbackNarrative(attempts), nil
	}

	release, ok := c.acquire()
	if !ok {
		return fallbackNarrative(attempts), nil
	}
	defer release()

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	text, err := c.call(callCtx, attempts)
	if err != nil {
		c.breaker.recordFailure()
		return fallbackNarrative(attempts), nil
	}
	c.breaker.recordSuccess()
	return text, nil
}

func (c *Client) call(ctx context.Context, attempts []domain.Attempt) (string, error) {
	resp, err := c.api.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 500,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildPrompt(attempts))),
		},
	})
	if err != nil {
		return "", err
	}
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			return tb.Text, nil
		}
	}
	return "", errors.New("intelligence: response had no text block")
}

func buildPrompt(attempts []domain.Attempt) string {
	var b strings.Builder
	b.WriteString("Write a short, plain-English paragraph (3-5 sentences) for a support " +
		"agent, summarizing what happened across these payment retry attempts for one " +
		"customer mandate. Use only the facts listed below; do not invent anything.\n\n")
	for _, a := range attempts {
		fmt.Fprintf(&b, "Attempt %d: scheduled for %s", a.Seq, a.ScheduledFor.Format(time.RFC3339))
		if a.FiredAt != nil {
			fmt.Fprintf(&b, ", fired at %s", a.FiredAt.Format(time.RFC3339))
		}
		if a.Outcome != nil {
			fmt.Fprintf(&b, ", outcome %s", *a.Outcome)
		}
		if a.FailureCode != nil {
			fmt.Fprintf(&b, ", failure code %s", *a.FailureCode)
		}
		fmt.Fprintf(&b, ", decision class %s (%s)\n", a.DecisionReason.Class, a.DecisionReason.PredictionBasis)
	}
	return b.String()
}

func fallbackNarrative(attempts []domain.Attempt) string {
	last := attempts[len(attempts)-1]
	status := "in progress"
	if last.Outcome != nil {
		status = string(*last.Outcome)
	}
	return fmt.Sprintf("%d attempt(s) on record. Most recent: attempt %d, status %s.",
		len(attempts), last.Seq, status)
}
