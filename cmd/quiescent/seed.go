package main

import (
	"context"
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/harness"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/spf13/cobra"
)

func newSeedCmd() *cobra.Command {
	var customers int
	var seed int64

	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Generate a batch of synthetic mandates for testing",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSeed(cmd.Context(), customers, seed)
		},
	}
	cmd.Flags().IntVar(&customers, "customers", 50, "number of synthetic customers, 6 cycles each")
	cmd.Flags().Int64Var(&seed, "seed", 42, "deterministic seed")
	return cmd
}

func runSeed(ctx context.Context, customers int, seed int64) error {
	s, err := store.Open(ctx, envString("DB_URL", ""))
	if err != nil {
		return err
	}
	defer s.Close()

	sequences := harness.GenerateCustomerSequences(seed, customers)
	created := 0
	for _, seq := range sequences {
		customerID := domain.NewCustomerID()
		for _, spec := range seq {
			c := domain.MandateCycle{
				CycleID:      domain.NewCycleID(),
				MandateID:    domain.NewMandateID(),
				CustomerID:   customerID,
				Rail:         spec.Rail,
				AmountPaise:  spec.AmountPaise,
				DueDate:      spec.DueDate,
				AttemptsUsed: 0,
				State:        domain.StatePending,
			}
			if err := s.CreateCycle(ctx, c); err != nil {
				return fmt.Errorf("cycle for customer %s: %w", customerID, err)
			}
			created++
		}
	}
	fmt.Printf("%s %s cycles across %s customers (seed %s)\n",
		green(bold("seeded")), teal(fmt.Sprint(created)), teal(fmt.Sprint(customers)), muted(fmt.Sprint(seed)))
	return nil
}
