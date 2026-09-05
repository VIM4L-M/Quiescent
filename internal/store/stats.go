package store

import (
	"context"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) FailureCodeStats(ctx context.Context, minOccurrences int64, limit int) ([]domain.FailureCodeStat, error) {
	if minOccurrences <= 0 {
		minOccurrences = 1
	}
	if limit <= 0 {
		limit = 20
	}
	const q = `
		SELECT a.failure_code AS code,
		       count(DISTINCT a.cycle_id) AS occurrences,
		       count(DISTINCT a.cycle_id) FILTER (WHERE c.state = 'recovered') AS recovered,
		       count(DISTINCT a.cycle_id) FILTER (WHERE c.state IN ('escalated', 'abandoned')) AS terminal
		  FROM attempts a
		  JOIN mandate_cycles c ON c.cycle_id = a.cycle_id
		 WHERE a.failure_code IS NOT NULL
		 GROUP BY a.failure_code
		HAVING count(DISTINCT a.cycle_id) >= $1
		 ORDER BY occurrences DESC
		 LIMIT $2`
	rows, err := s.q.Query(ctx, q, minOccurrences, limit)
	if err != nil {
		return nil, mapError("store: failure code stats", err)
	}
	stats, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.FailureCodeStat])
	if err != nil {
		return nil, mapError("store: failure code stats", err)
	}
	return stats, nil
}
