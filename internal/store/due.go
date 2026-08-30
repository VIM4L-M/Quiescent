package store

import (
	"context"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) DueAttempts(ctx context.Context, limit int) ([]domain.Attempt, error) {
	if limit <= 0 {
		limit = 20
	}
	const q = `SELECT ` + attemptColumns + `
		  FROM attempts
		 WHERE scheduled_for <= now() AND outcome IS NULL
		 ORDER BY scheduled_for
		 LIMIT $1`
	rows, err := s.q.Query(ctx, q, limit)
	if err != nil {
		return nil, mapError("store: due attempts", err)
	}
	attempts, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.Attempt])
	if err != nil {
		return nil, mapError("store: due attempts", err)
	}
	return attempts, nil
}
