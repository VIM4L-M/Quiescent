package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) AcquireLease(ctx context.Context, cycleID domain.CycleID, holder string, ttl time.Duration) (domain.Fence, error) {
	if cycleID == "" {
		return 0, fmt.Errorf("%w: cycleID is required", ErrInvalidArgument)
	}
	if holder == "" {
		return 0, fmt.Errorf("%w: holder is required", ErrInvalidArgument)
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("%w: ttl must be positive", ErrInvalidArgument)
	}
	const q = `
		UPDATE leases
		   SET holder = $2, fence = fence + 1,
		       expires_at = now() + make_interval(secs => $3)
		 WHERE cycle_id = $1 AND expires_at < now()
		RETURNING fence`
	var fence int64
	err := s.q.QueryRow(ctx, q, cycleID, holder, ttl.Seconds()).Scan(&fence)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("store: acquire lease: %w", ErrConflict)
		}
		return 0, mapError("store: acquire lease", err)
	}
	return domain.Fence(fence), nil
}
