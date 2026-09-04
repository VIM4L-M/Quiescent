package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ReserveAttempt(ctx context.Context, cycleID domain.CycleID, expectedVersion int64,
	a domain.Attempt, noticePayload json.RawMessage, deliverBy time.Time) error {

	if cycleID == "" {
		return fmt.Errorf("%w: cycleID is required", ErrInvalidArgument)
	}
	return s.Tx(ctx, func(tx *Store) error {
		const reserveQ = `
			UPDATE mandate_cycles
			   SET attempts_used = attempts_used + 1, version = version + 1, state = 'scheduled'
			 WHERE cycle_id = $1 AND version = $2 AND attempts_used < $3
			RETURNING attempts_used`
		var used int16
		err := tx.q.QueryRow(ctx, reserveQ, cycleID, expectedVersion, domain.MaxAttempts).Scan(&used)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("store: reserve attempt: %w", ErrConflict)
			}
			return mapError("store: reserve attempt", err)
		}
		if err := tx.InsertAttempt(ctx, a); err != nil {
			return err
		}
		return tx.QueueNotice(ctx, cycleID, a.AttemptID, domain.OutboxPreDebitNotice, noticePayload, deliverBy)
	})
}
