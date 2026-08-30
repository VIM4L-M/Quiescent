package lease_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/lease"
	"github.com/VIM4L-M/Quiescent/internal/store"
)

func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func testStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		t.Skip("DB_URL not set")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	return s, ctx
}

func seedCycle(t *testing.T, s *store.Store, ctx context.Context) domain.MandateCycle {
	t.Helper()
	c := domain.MandateCycle{
		CycleID:      domain.CycleID(newUUID(t)),
		MandateID:    domain.MandateID(newUUID(t)),
		CustomerID:   domain.CustomerID(newUUID(t)),
		Rail:         domain.RailUPIAutopay,
		AmountPaise:  200000,
		DueDate:      time.Now().UTC(),
		AttemptsUsed: 0,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	return c
}

func TestAcquireReturnsHandleWithFence(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	h, err := lease.Acquire(ctx, s, c.CycleID, "worker-a", 30*time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if h.CycleID != c.CycleID || h.Holder != "worker-a" || h.Fence != 1 {
		t.Fatalf("handle: %+v", h)
	}
}

func TestAcquireFailsWhenAlreadyHeld(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	if _, err := lease.Acquire(ctx, s, c.CycleID, "worker-a", 30*time.Second); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := lease.Acquire(ctx, s, c.CycleID, "worker-b", 30*time.Second); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second acquire: want ErrConflict, got %v", err)
	}
}

func TestC4StalledWorkerHandleNeverRefreshes(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	stalled, err := lease.Acquire(ctx, s, c.CycleID, "worker-a", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("stalled worker's acquire: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	fresh, err := lease.Acquire(ctx, s, c.CycleID, "worker-b", 30*time.Second)
	if err != nil {
		t.Fatalf("fresh worker's acquire: %v", err)
	}
	if fresh.Fence <= stalled.Fence {
		t.Fatalf("fence did not advance: stalled=%d fresh=%d", stalled.Fence, fresh.Fence)
	}
	if stalled.Fence != 1 {
		t.Fatalf("stalled worker's own handle must keep the fence it captured, got %d", stalled.Fence)
	}
}
