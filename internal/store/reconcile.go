package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) MarkCycleRecovered(ctx context.Context, cycleID domain.CycleID) error {
	const q = `UPDATE mandate_cycles SET state = 'recovered' WHERE cycle_id = $1`
	tag, err := s.q.Exec(ctx, q, cycleID)
	return expectOne("store: mark cycle recovered", tag, err)
}

func (s *Store) HoldCycle(ctx context.Context, cycleID domain.CycleID) error {
	const q = `UPDATE mandate_cycles SET state = 'held' WHERE cycle_id = $1`
	tag, err := s.q.Exec(ctx, q, cycleID)
	return expectOne("store: hold cycle", tag, err)
}

func (s *Store) ResolveDebited(ctx context.Context, attemptID domain.AttemptID) error {
	if attemptID == "" {
		return fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	return s.Tx(ctx, func(tx *Store) error {
		const markQ = `
			UPDATE attempts
			   SET outcome = 'SUCCESS'
			 WHERE attempt_id = $1 AND outcome = 'TIMEOUT'
			RETURNING cycle_id`
		var cycleID domain.CycleID
		err := tx.q.QueryRow(ctx, markQ, attemptID).Scan(&cycleID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("store: resolve debited: %w", ErrConflict)
			}
			return mapError("store: resolve debited", err)
		}
		return tx.MarkCycleRecovered(ctx, cycleID)
	})
}

func (s *Store) ResolveNotDebited(ctx context.Context, attemptID domain.AttemptID) error {
	if attemptID == "" {
		return fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	return s.Tx(ctx, func(tx *Store) error {
		const markQ = `
			UPDATE attempts
			   SET outcome = 'ABANDONED_STALE'
			 WHERE attempt_id = $1 AND outcome = 'TIMEOUT'
			RETURNING cycle_id`
		var cycleID domain.CycleID
		err := tx.q.QueryRow(ctx, markQ, attemptID).Scan(&cycleID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("store: resolve not debited: %w", ErrConflict)
			}
			return mapError("store: resolve not debited", err)
		}
		const refundQ = `
			UPDATE mandate_cycles
			   SET attempts_used = attempts_used - 1, state = 'pending'
			 WHERE cycle_id = $1 AND attempts_used > 0`
		tag, err := tx.q.Exec(ctx, refundQ, cycleID)
		return expectOne("store: resolve not debited refund", tag, err)
	})
}
