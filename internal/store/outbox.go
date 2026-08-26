package store

import (
	"context"
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

const outboxColumns = `id, cycle_id, attempt_id, kind, payload,
	deliver_by, delivered_at, attempts`

func (s *Store) NoticeDelivered(ctx context.Context, attemptID domain.AttemptID) (bool, error) {
	if attemptID == "" {
		return false, fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM outbox
			 WHERE attempt_id = $1
			   AND kind = $2
			   AND delivered_at IS NOT NULL)`
	var delivered bool
	err := s.q.QueryRow(ctx, q, attemptID, domain.OutboxPreDebitNotice).Scan(&delivered)
	if err != nil {
		return false, mapError("store: notice gate", err)
	}
	return delivered, nil
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
