package store

import (
	"context"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func (s *Store) InvariantDoubleDebits(ctx context.Context) ([]domain.CycleID, error) {
	const q = `
		SELECT cycle_id FROM attempts WHERE outcome = 'SUCCESS'
		 GROUP BY cycle_id HAVING count(*) > 1`
	return queryCycleIDs(ctx, s, q)
}

func (s *Store) InvariantBudgetExceeded(ctx context.Context) ([]domain.CycleID, error) {
	const q = `SELECT cycle_id FROM mandate_cycles WHERE attempts_used > 4`
	return queryCycleIDs(ctx, s, q)
}

func (s *Store) InvariantOrphaned(ctx context.Context) ([]domain.CycleID, error) {
	const q = `
		SELECT cycle_id FROM mandate_cycles
		 WHERE state NOT IN ('recovered', 'escalated', 'abandoned', 'held')
		   AND updated_at < now() - interval '1 hour'`
	return queryCycleIDs(ctx, s, q)
}

func (s *Store) InvariantBudgetMismatch(ctx context.Context) ([]domain.CycleID, error) {
	const q = `
		SELECT c.cycle_id FROM mandate_cycles c
		 WHERE c.attempts_used <> (
		   SELECT count(*) FROM attempts a
		    WHERE a.cycle_id = c.cycle_id
		      AND (a.outcome IS NULL OR a.outcome <> 'ABANDONED_STALE'))`
	return queryCycleIDs(ctx, s, q)
}

func (s *Store) InvariantBlockedWindowFired(ctx context.Context) ([]domain.AttemptID, error) {
	const q = `
		SELECT attempt_id FROM attempts
		 WHERE (fired_at AT TIME ZONE 'Asia/Kolkata')::time BETWEEN '10:00' AND '13:00'
		    OR (fired_at AT TIME ZONE 'Asia/Kolkata')::time BETWEEN '17:00' AND '21:30'`
	return queryAttemptIDs(ctx, s, q)
}

func (s *Store) InvariantMissingNotice(ctx context.Context) ([]domain.AttemptID, error) {
	const q = `
		SELECT a.attempt_id FROM attempts a
		  LEFT JOIN outbox o
		    ON o.attempt_id = a.attempt_id AND o.kind = 'pre_debit_notice'
		 WHERE a.fired_at IS NOT NULL
		   AND (o.delivered_at IS NULL OR o.delivered_at > a.fired_at - interval '24 hours')`
	return queryAttemptIDs(ctx, s, q)
}

func queryCycleIDs(ctx context.Context, s *Store, q string) ([]domain.CycleID, error) {
	rows, err := s.q.Query(ctx, q)
	if err != nil {
		return nil, mapError("store: invariant", err)
	}
	defer rows.Close()
	var out []domain.CycleID
	for rows.Next() {
		var id domain.CycleID
		if err := rows.Scan(&id); err != nil {
			return nil, mapError("store: invariant", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("store: invariant", err)
	}
	return out, nil
}

func queryAttemptIDs(ctx context.Context, s *Store, q string) ([]domain.AttemptID, error) {
	rows, err := s.q.Query(ctx, q)
	if err != nil {
		return nil, mapError("store: invariant", err)
	}
	defer rows.Close()
	var out []domain.AttemptID
	for rows.Next() {
		var id domain.AttemptID
		if err := rows.Scan(&id); err != nil {
			return nil, mapError("store: invariant", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("store: invariant", err)
	}
	return out, nil
}
