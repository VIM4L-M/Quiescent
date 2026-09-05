package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/VIM4L-M/Quiescent/internal/verify"
	"github.com/spf13/cobra"
)

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Run the six correctness invariants against the live database",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(cmd.Context())
		},
	}
}

func runVerify(ctx context.Context) error {
	dbURL := envString("DB_URL", "")
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer s.Close()

	fmt.Printf("%s %s...\n\n", muted("Running 6 invariants against"), muted(dbURL))
	results, err := verify.Run(ctx, s)
	if err != nil {
		return err
	}

	failed := 0
	for _, r := range results {
		if r.Passed() {
			fmt.Printf("  %s %d. %s\n", green(bold("[PASS]")), r.Number, r.Name)
			continue
		}
		failed++
		fmt.Printf("  %s %d. %s %s\n", red(bold("[FAIL]")), r.Number, r.Name,
			muted(fmt.Sprintf("(%d row(s): %s)", len(r.Violations), strings.Join(r.Violations, ", "))))
	}

	summary := fmt.Sprintf("%d/%d passed", len(results)-failed, len(results))
	if failed > 0 {
		fmt.Printf("\n%s, %s\n", green(summary), red(fmt.Sprintf("%d failed", failed)))
		return fmt.Errorf("%d invariant(s) failed", failed)
	}
	fmt.Printf("\n%s\n", green(bold(summary)))
	return nil
}
