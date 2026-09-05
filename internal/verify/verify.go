package verify

import (
	"context"
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/store"
)

type Result struct {
	Number     int
	Name       string
	Violations []string
}

func (r Result) Passed() bool {
	return len(r.Violations) == 0
}

func Run(ctx context.Context, s *store.Store) ([]Result, error) {
	results := make([]Result, 0, 6)

	cycles, err := s.InvariantDoubleDebits(ctx)
	if err != nil {
		return nil, fmt.Errorf("invariant 1: %w", err)
	}
	results = append(results, Result{1, "No cycle debited more than once", stringifyIDs(cycles)})

	cycles, err = s.InvariantBudgetExceeded(ctx)
	if err != nil {
		return nil, fmt.Errorf("invariant 2: %w", err)
	}
	results = append(results, Result{2, "attempts_used never exceeds 4", stringifyIDs(cycles)})

	cycles, err = s.InvariantOrphaned(ctx)
	if err != nil {
		return nil, fmt.Errorf("invariant 3: %w", err)
	}
	results = append(results, Result{3, "Every cycle reaches a terminal disposition (or is held)", stringifyIDs(cycles)})

	cycles, err = s.InvariantBudgetMismatch(ctx)
	if err != nil {
		return nil, fmt.Errorf("invariant 4: %w", err)
	}
	results = append(results, Result{4, "Budget agrees with fired attempts", stringifyIDs(cycles)})

	attempts, err := s.InvariantBlockedWindowFired(ctx)
	if err != nil {
		return nil, fmt.Errorf("invariant 5: %w", err)
	}
	results = append(results, Result{5, "No attempt fired into a blocked execution window", stringifyIDs(attempts)})

	attempts, err = s.InvariantMissingNotice(ctx)
	if err != nil {
		return nil, fmt.Errorf("invariant 6: %w", err)
	}
	results = append(results, Result{6, "No attempt fired without a notice delivered 24h ahead", stringifyIDs(attempts)})

	return results, nil
}

func AllPassed(results []Result) bool {
	for _, r := range results {
		if !r.Passed() {
			return false
		}
	}
	return true
}

func stringifyIDs[T ~string](ids []T) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}
