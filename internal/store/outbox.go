package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

const outboxColumns = `id, cycle_id, attempt_id, kind, payload,
	deliver_by, delivered_at, attempts`

func (s *Store) QueueNotice(ctx context.Context, cycleID domain.CycleID, attemptID domain.AttemptID,
	kind domain.OutboxKind, payload json.RawMessage, deliverBy time.Time) error {

	if !kind.Valid() {
		return fmt.Errorf("%w: outbox kind %q", ErrInvalidEnum, kind)
	}
	const q = `
		INSERT INTO outbox (cycle_id, attempt_id, kind, payload, deliver_by)
		VALUES ($1, $2, $3, $4, $5)`
	_, err := s.q.Exec(ctx, q, cycleID, attemptID, kind, payload, deliverBy)
	return mapError("store: queue notice", err)
}

func (s *Store) MarkNoticeDelivered(ctx context.Context, attemptID domain.AttemptID, kind domain.OutboxKind) error {
	if !kind.Valid() {
		return fmt.Errorf("%w: outbox kind %q", ErrInvalidEnum, kind)
	}
	const q = `
		UPDATE outbox SET delivered_at = now()
		 WHERE attempt_id = $1 AND kind = $2 AND delivered_at IS NULL`
	tag, err := s.q.Exec(ctx, q, attemptID, kind)
	return expectOne("store: mark notice delivered", tag, err)
}

func (s *Store) NoticeDelivered(ctx context.Context, attemptID domain.AttemptID, scheduledFor time.Time) (bool, error) {
	if attemptID == "" {
		return false, fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	deadline := scheduledFor.Add(-domain.NoticeLead)
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM outbox
			 WHERE attempt_id = $1
			   AND kind = $2
			   AND delivered_at IS NOT NULL
			   AND delivered_at <= $3)`
	var delivered bool
	err := s.q.QueryRow(ctx, q, attemptID, domain.OutboxPreDebitNotice, deadline).Scan(&delivered)
	if err != nil {
		return false, mapError("store: notice gate", err)
	}
	return delivered, nil
}

func (s *Store) PendingNotices(ctx context.Context, limit int) ([]domain.OutboxEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `SELECT ` + outboxColumns + `
		  FROM outbox
		 WHERE delivered_at IS NULL
		 ORDER BY deliver_by
		 LIMIT $1`
	rows, err := s.q.Query(ctx, q, limit)
	if err != nil {
		return nil, mapError("store: pending notices", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.OutboxEntry])
	if err != nil {
		return nil, mapError("store: pending notices", err)
	}
	return entries, nil
}

func (s *Store) OutboxByAttempt(ctx context.Context, attemptID domain.AttemptID,
	kind domain.OutboxKind) ([]domain.OutboxEntry, error) {

	if !kind.Valid() {
		return nil, fmt.Errorf("%w: outbox kind %q", ErrInvalidEnum, kind)
	}
	const q = `SELECT ` + outboxColumns + `
		  FROM outbox
		 WHERE attempt_id = $1 AND kind = $2
		 ORDER BY id`
	rows, err := s.q.Query(ctx, q, attemptID, kind)
	if err != nil {
		return nil, mapError("store: outbox by attempt", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.OutboxEntry])
	if err != nil {
		return nil, mapError("store: outbox by attempt", err)
	}
	return entries, nil
}
