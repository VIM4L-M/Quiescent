package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

const attemptColumns = `attempt_id, cycle_id, seq, idempotency_key, fence,
	scheduled_for, fired_at, outcome, failure_code, decision_reason`

func (s *Store) InsertAttempt(ctx context.Context, a domain.Attempt) error {
	if err := validateAttempt(a); err != nil {
		return err
	}
	const q = `
		INSERT INTO attempts
			(attempt_id, cycle_id, seq, idempotency_key, scheduled_for, decision_reason)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := s.q.Exec(ctx, q,
		a.AttemptID, a.CycleID, a.Seq, a.IdempotencyKey, a.ScheduledFor, a.DecisionReason)
	return mapError("store: insert attempt", err)
}

func (s *Store) MarkAttemptFired(ctx context.Context, attemptID domain.AttemptID, fence domain.Fence) error {
	if attemptID == "" {
		return fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	if fence <= 0 {
		return fmt.Errorf("%w: fence must be positive", ErrInvalidArgument)
	}
	return s.Tx(ctx, func(tx *Store) error {
		const markQ = `
			UPDATE attempts
			   SET fired_at = now(), fence = $2
			 WHERE attempt_id = $1
			   AND fired_at IS NULL
			RETURNING cycle_id`
		var cycleID domain.CycleID
		err := tx.q.QueryRow(ctx, markQ, attemptID, int64(fence)).Scan(&cycleID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("store: mark attempt fired: %w", ErrConflict)
			}
			return mapError("store: mark attempt fired", err)
		}
		const stateQ = `UPDATE mandate_cycles SET state = 'in_flight' WHERE cycle_id = $1`
		tag, err := tx.q.Exec(ctx, stateQ, cycleID)
		return expectOne("store: mark attempt fired state", tag, err)
	})
}

func (s *Store) RecordAttemptOutcome(ctx context.Context, attemptID domain.AttemptID,
	outcome domain.Outcome, failureCode *domain.FailureCode) error {

	if attemptID == "" {
		return fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	if err := validateOutcome(outcome, failureCode); err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *Store) error {
		const q = `
			UPDATE attempts
			   SET outcome = $2, failure_code = $3
			 WHERE attempt_id = $1
			   AND outcome IS NULL
			   AND (($2::text =  'ABANDONED_STALE' AND fired_at IS     NULL)
			     OR ($2::text <> 'ABANDONED_STALE' AND fired_at IS NOT NULL))
			RETURNING cycle_id`
		var cycleID domain.CycleID
		err := tx.q.QueryRow(ctx, q, attemptID, outcome, failureCode).Scan(&cycleID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("store: record attempt outcome: %w", ErrConflict)
			}
			return mapError("store: record attempt outcome", err)
		}
		if outcome != domain.OutcomeTimeout {
			return nil
		}
		const stateQ = `UPDATE mandate_cycles SET state = 'unknown' WHERE cycle_id = $1`
		tag, err := tx.q.Exec(ctx, stateQ, cycleID)
		return expectOne("store: record attempt outcome state", tag, err)
	})
}

func (s *Store) ResolveAttemptOutcome(ctx context.Context, attemptID domain.AttemptID,
	outcome domain.Outcome, failureCode *domain.FailureCode) error {

	if attemptID == "" {
		return fmt.Errorf("%w: attemptID is required", ErrInvalidArgument)
	}
	if outcome != domain.OutcomeSuccess && outcome != domain.OutcomeFailure {
		return fmt.Errorf("%w: a timeout resolves only to SUCCESS or FAILURE, got %q",
			ErrInvalidArgument, outcome)
	}
	if err := validateOutcome(outcome, failureCode); err != nil {
		return err
	}
	const q = `
		UPDATE attempts
		   SET outcome = $2, failure_code = $3
		 WHERE attempt_id = $1
		   AND outcome = 'TIMEOUT'`
	tag, err := s.q.Exec(ctx, q, attemptID, outcome, failureCode)
	return expectOne("store: resolve attempt outcome", tag, err)
}

func (s *Store) Attempt(ctx context.Context, attemptID domain.AttemptID) (domain.Attempt, error) {
	const q = `SELECT ` + attemptColumns + ` FROM attempts WHERE attempt_id = $1`
	rows, err := s.q.Query(ctx, q, attemptID)
	if err != nil {
		return domain.Attempt{}, mapError("store: load attempt", err)
	}
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[domain.Attempt])
	if err != nil {
		return domain.Attempt{}, mapError("store: load attempt", err)
	}
	return a, nil
}

func (s *Store) AttemptsByCycle(ctx context.Context, cycleID domain.CycleID) ([]domain.Attempt, error) {
	const q = `SELECT ` + attemptColumns + ` FROM attempts WHERE cycle_id = $1 ORDER BY seq`
	rows, err := s.q.Query(ctx, q, cycleID)
	if err != nil {
		return nil, mapError("store: attempts by cycle", err)
	}
	attempts, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.Attempt])
	if err != nil {
		return nil, mapError("store: attempts by cycle", err)
	}
	return attempts, nil
}
