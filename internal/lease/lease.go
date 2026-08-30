package lease

import (
	"context"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

type Handle struct {
	CycleID domain.CycleID
	Holder  string
	Fence   domain.Fence
}

func Acquire(ctx context.Context, s *store.Store, cycleID domain.CycleID, holder string, ttl time.Duration) (Handle, error) {
	fence, err := s.AcquireLease(ctx, cycleID, holder, ttl)
	if err != nil {
		return Handle{}, err
	}
	return Handle{CycleID: cycleID, Holder: holder, Fence: fence}, nil
}
