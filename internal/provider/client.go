package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

var (
	ErrStaleFence          = errors.New("provider: stale fence")
	ErrIdempotencyConflict = errors.New("provider: idempotency conflict")
	ErrMalformedRequest    = errors.New("provider: malformed request")
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: timeout}}
}

func (c *Client) Debit(ctx context.Context, in DebitRequest) (DebitResponse, error) {
	var out DebitResponse
	body, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/debit", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return out, decodeError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *Client) Status(ctx context.Context, idempotencyKey string) (DebitResponse, bool, error) {
	var out DebitResponse
	v := url.Values{}
	v.Set("idempotencyKey", idempotencyKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/status?"+v.Encode(), nil)
	if err != nil {
		return out, false, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return out, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return out, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return out, false, decodeError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, false, err
	}
	return out, true, nil
}

func decodeError(resp *http.Response) error {
	var body ErrorBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	switch body.Error {
	case ErrCodeStaleFence:
		return fmt.Errorf("%w: %s", ErrStaleFence, body.Message)
	case ErrCodeIdempotencyConflict:
		return fmt.Errorf("%w: %s", ErrIdempotencyConflict, body.Message)
	case ErrCodeMalformedRequest:
		return fmt.Errorf("%w: %s", ErrMalformedRequest, body.Message)
	}
	return fmt.Errorf("provider: unexpected status %d: %s", resp.StatusCode, body.Message)
}
