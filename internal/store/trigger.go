package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

const triggerColumns = `trigger_id, cycle_id, seq, sent_at, expires_at, responded_at, response`

func (s *Store) QueueTrigger(ctx context.Context, cycleID domain.CycleID, seq int16, sentAt, expiresAt time.Time) error {
	if cycleID == "" {
		return fmt.Errorf("%w: cycleID is required", ErrInvalidArgument)
	}
	const q = `
		INSERT INTO balance_triggers (trigger_id, cycle_id, seq, sent_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (cycle_id, seq) DO NOTHING`
	_, err := s.q.Exec(ctx, q, domain.NewTriggerID(), cycleID, seq, sentAt, expiresAt)
	return mapError("store: queue trigger", err)
}

func (s *Store) RespondTrigger(ctx context.Context, cycleID domain.CycleID, seq int16, response string, respondedAt time.Time) error {
	if response != "yes" && response != "no" {
		return fmt.Errorf("%w: response must be yes or no, got %q", ErrInvalidArgument, response)
	}
	const q = `
		UPDATE balance_triggers
		   SET responded_at = $3, response = $4
		 WHERE cycle_id = $1 AND seq = $2 AND responded_at IS NULL`
	tag, err := s.q.Exec(ctx, q, cycleID, seq, respondedAt, response)
	return expectOne("store: respond trigger", tag, err)
}

func (s *Store) TriggerFor(ctx context.Context, cycleID domain.CycleID, seq int16) (*domain.BalanceTrigger, error) {
	const q = `SELECT ` + triggerColumns + `
		  FROM balance_triggers
		 WHERE cycle_id = $1 AND seq = $2`
	rows, err := s.q.Query(ctx, q, cycleID, seq)
	if err != nil {
		return nil, mapError("store: trigger for", err)
	}
	trig, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[domain.BalanceTrigger])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, mapError("store: trigger for", err)
	}
	return &trig, nil
}

func (s *Store) PendingTriggerForCycle(ctx context.Context, cycleID domain.CycleID) (*domain.BalanceTrigger, error) {
	const q = `SELECT ` + triggerColumns + `
		  FROM balance_triggers
		 WHERE cycle_id = $1 AND responded_at IS NULL
		 ORDER BY seq DESC
		 LIMIT 1`
	rows, err := s.q.Query(ctx, q, cycleID)
	if err != nil {
		return nil, mapError("store: pending trigger for cycle", err)
	}
	trig, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[domain.BalanceTrigger])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, mapError("store: pending trigger for cycle", err)
	}
	return &trig, nil
}
