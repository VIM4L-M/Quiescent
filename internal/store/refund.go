package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) AbandonAttempt(ctx context.Context, attemptID domain.AttemptID) error {
	if attemptID == "" {
		return fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	return s.Tx(ctx, func(tx *Store) error {
		const markQ = `
			UPDATE attempts
			   SET outcome = 'ABANDONED_STALE'
			 WHERE attempt_id = $1
			   AND outcome IS NULL
			   AND fired_at IS NULL
			RETURNING cycle_id`
		var cycleID domain.CycleID
		err := tx.q.QueryRow(ctx, markQ, attemptID).Scan(&cycleID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("store: abandon attempt: %w", ErrConflict)
			}
			return mapError("store: abandon attempt", err)
		}
		const refundQ = `
			UPDATE mandate_cycles
			   SET attempts_used = attempts_used - 1, state = 'pending'
			 WHERE cycle_id = $1 AND attempts_used > 0`
		tag, err := tx.q.Exec(ctx, refundQ, cycleID)
		return expectOne("store: abandon attempt refund", tag, err)
	})
}
