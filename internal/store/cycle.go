package store

import (
	"context"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

const cycleColumns = `cycle_id, mandate_id, customer_id, rail, amount_paise,
	due_date, attempts_used, state, version, updated_at`

func (s *Store) CreateCycle(ctx context.Context, c domain.MandateCycle) error {
	if err := validateCycle(c); err != nil {
		return err
	}
	const q = `
		INSERT INTO mandate_cycles
			(cycle_id, mandate_id, customer_id, rail, amount_paise, due_date, attempts_used, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := s.q.Exec(ctx, q,
		c.CycleID, c.MandateID, c.CustomerID, c.Rail,
		c.AmountPaise, c.DueDate, c.AttemptsUsed, c.State)
	return mapError("store: create cycle", err)
}

func (s *Store) Cycle(ctx context.Context, cycleID domain.CycleID) (domain.MandateCycle, error) {
	const q = `SELECT ` + cycleColumns + ` FROM mandate_cycles WHERE cycle_id = $1`
	rows, err := s.q.Query(ctx, q, cycleID)
	if err != nil {
		return domain.MandateCycle{}, mapError("store: load cycle", err)
	}
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[domain.MandateCycle])
	if err != nil {
		return domain.MandateCycle{}, mapError("store: load cycle", err)
	}
	return c, nil
}

func (s *Store) CyclesByState(ctx context.Context, state domain.State, limit int) ([]domain.MandateCycle, error) {
	if !state.Valid() {
		return nil, mapError("store: cycles by state", ErrInvalidEnum)
	}
	if limit <= 0 {
		limit = 100
	}
	const q = `SELECT ` + cycleColumns + `
		  FROM mandate_cycles
		 WHERE state = $1
		 ORDER BY due_date, cycle_id
		 LIMIT $2`
	rows, err := s.q.Query(ctx, q, state, limit)
	if err != nil {
		return nil, mapError("store: cycles by state", err)
	}
	cycles, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.MandateCycle])
	if err != nil {
		return nil, mapError("store: cycles by state", err)
	}
	return cycles, nil
}
