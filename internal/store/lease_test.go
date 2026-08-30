package store

import (
	"errors"
	"testing"
	"time"
)

func TestAcquireLeaseSucceedsWhenUnheld(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	fence, err := s.AcquireLease(ctx, c.CycleID, "worker-a", 30*time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if fence != 1 {
		t.Fatalf("fence: got %d want 1", fence)
	}
}

func TestAcquireLeaseFailsWhenAlreadyHeld(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	if _, err := s.AcquireLease(ctx, c.CycleID, "worker-a", 30*time.Second); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := s.AcquireLease(ctx, c.CycleID, "worker-b", 30*time.Second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second acquire: want ErrConflict, got %v", err)
	}
}

func TestC4FenceIsMonotonicAcrossExpiry(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	first, err := s.AcquireLease(ctx, c.CycleID, "worker-a", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	second, err := s.AcquireLease(ctx, c.CycleID, "worker-b", 30*time.Second)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if second <= first {
		t.Fatalf("fence did not advance across expiry: first=%d second=%d", first, second)
	}
}

func TestAcquireLeaseRejectsInvalidArguments(t *testing.T) {
	s, ctx := testStore(t)
	c := seedCycle(t, s, ctx)

	if _, err := s.AcquireLease(ctx, "", "worker-a", 30*time.Second); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty cycleID: want ErrInvalidArgument, got %v", err)
	}
	if _, err := s.AcquireLease(ctx, c.CycleID, "", 30*time.Second); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty holder: want ErrInvalidArgument, got %v", err)
	}
	if _, err := s.AcquireLease(ctx, c.CycleID, "worker-a", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero ttl: want ErrInvalidArgument, got %v", err)
	}
}
