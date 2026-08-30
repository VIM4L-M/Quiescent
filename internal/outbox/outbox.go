package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

type Sender interface {
	Send(ctx context.Context, cycleID domain.CycleID, attemptID domain.AttemptID,
		kind domain.OutboxKind, payload json.RawMessage) error
}

type LogSender struct {
	Log *slog.Logger
}

func (s LogSender) Send(ctx context.Context, cycleID domain.CycleID, attemptID domain.AttemptID,
	kind domain.OutboxKind, payload json.RawMessage) error {

	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info("notice sent", "cycleID", cycleID, "attemptID", attemptID, "kind", kind, "payload", string(payload))
	return nil
}

type Result string

const (
	ResultDelivered  Result = "delivered"
	ResultTooLate    Result = "too_late"
	ResultSendFailed Result = "send_failed"
)

type Relay struct {
	Store  *store.Store
	Sender Sender
	Log    *slog.Logger
	Now    func() time.Time
}

func New(s *store.Store, sender Sender, log *slog.Logger) *Relay {
	if log == nil {
		log = slog.Default()
	}
	return &Relay{Store: s, Sender: sender, Log: log, Now: func() time.Time { return time.Now().UTC() }}
}

func (r *Relay) ProcessOne(ctx context.Context, entry domain.OutboxEntry) (Result, error) {
	if r.Now().After(entry.DeliverBy) {
		if err := r.Store.AbandonAttempt(ctx, entry.AttemptID); err != nil && !errors.Is(err, store.ErrConflict) {
			return "", err
		}
		r.Log.Warn("notice missed its 24h deadline; cancelling and refunding rather than sending it late",
			"cycleID", entry.CycleID, "attemptID", entry.AttemptID, "deliverBy", entry.DeliverBy)
		return ResultTooLate, nil
	}

	if err := r.Sender.Send(ctx, entry.CycleID, entry.AttemptID, entry.Kind, entry.Payload); err != nil {
		r.Log.Warn("notice send failed, will retry before the deadline",
			"cycleID", entry.CycleID, "attemptID", entry.AttemptID, "error", err)
		return ResultSendFailed, nil
	}

	if err := r.Store.MarkNoticeDelivered(ctx, entry.AttemptID, entry.Kind); err != nil {
		return "", err
	}
	return ResultDelivered, nil
}

func (r *Relay) ProcessBatch(ctx context.Context, limit int) ([]Result, error) {
	entries, err := r.Store.PendingNotices(ctx, limit)
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(entries))
	for i, entry := range entries {
		result, err := r.ProcessOne(ctx, entry)
		if err != nil {
			return results, err
		}
		results[i] = result
	}
	return results, nil
}
