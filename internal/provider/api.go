package provider

import (
	"strconv"
	"strings"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

type DebitRequest struct {
	CycleID        domain.CycleID `json:"cycleID"`
	IdempotencyKey string         `json:"idempotencyKey"`
	Fence          int64          `json:"fence"`
	AmountPaise    int64          `json:"amountPaise"`
	Rail           domain.Rail    `json:"rail"`
}

type DebitResponse struct {
	IdempotencyKey string             `json:"idempotencyKey"`
	CycleID        domain.CycleID     `json:"cycleID"`
	AttemptNumber  int                `json:"attemptNumber"`
	Outcome        domain.Outcome     `json:"outcome"`
	FailureCode    domain.FailureCode `json:"failureCode,omitempty"`
	AmountPaise    int64              `json:"amountPaise"`
	BouncePaise    int64              `json:"bouncePaise,omitempty"`
	DebitedAt      time.Time          `json:"debitedAt"`
	Replayed       bool               `json:"replayed"`
}

type InjectRequest struct {
	Mode       InjectMode     `json:"mode"`
	CycleID    domain.CycleID `json:"cycleID,omitempty"`
	DurationMS int64          `json:"durationMs,omitempty"`
	Count      int            `json:"count,omitempty"`
}

type ErrorCode string

const (
	ErrCodeStaleFence          ErrorCode = "STALE_FENCE"
	ErrCodeIdempotencyConflict ErrorCode = "IDEMPOTENCY_CONFLICT"
	ErrCodeMalformedRequest    ErrorCode = "MALFORMED_REQUEST"
	ErrCodeUnknownKey          ErrorCode = "UNKNOWN_KEY"
)

type ErrorBody struct {
	Error   ErrorCode `json:"error"`
	Message string    `json:"message"`
}

func IdempotencyKey(cycleID domain.CycleID, seq int) string {
	return string(cycleID) + ":" + strconv.Itoa(seq)
}

func AttemptNumberFromKey(cycleID domain.CycleID, key string) (int, bool) {
	prefix := string(cycleID) + ":"
	if !strings.HasPrefix(key, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(key[len(prefix):])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
