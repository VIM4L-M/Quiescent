package store

import (
	"context"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func (s *Store) CustomerSuccessHistory(ctx context.Context, customerID domain.CustomerID, limit int) ([]time.Time, error) {
	if customerID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	const q = `
		SELECT a.fired_at
		  FROM attempts a
		  JOIN mandate_cycles c ON c.cycle_id = a.cycle_id
		 WHERE c.customer_id = $1
		   AND a.outcome = 'SUCCESS'
		   AND a.fired_at IS NOT NULL
		 ORDER BY a.fired_at DESC
		 LIMIT $2`
	rows, err := s.q.Query(ctx, q, customerID, limit)
	if err != nil {
		return nil, mapError("store: customer success history", err)
	}
	defer rows.Close()

	var times []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, mapError("store: customer success history", err)
		}
		times = append(times, t)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("store: customer success history", err)
	}
	return times, nil
}
