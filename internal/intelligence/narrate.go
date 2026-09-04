package intelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
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

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	MaxCompletionTokens int           `json:"max_completion_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) call(ctx context.Context, attempts []domain.Attempt) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:               c.model,
		Messages:            []chatMessage{{Role: "user", Content: buildPrompt(attempts)}},
		MaxCompletionTokens: 500,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil {
			return "", fmt.Errorf("intelligence: groq %d: %s", resp.StatusCode, out.Error.Message)
		}
		return "", fmt.Errorf("intelligence: groq returned %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("intelligence: no choices in response")
	}
	return out.Choices[0].Message.Content, nil
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
