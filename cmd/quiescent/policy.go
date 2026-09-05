package main

import (
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/harness"
	"github.com/spf13/cobra"
)

func newPolicyCmd() *cobra.Command {
	var seed int64
	var customers int

	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Compare this system's policy against the naive baseline, one seed",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPolicy(seed, customers)
		},
	}
	cmd.Flags().Int64Var(&seed, "seed", 1, "deterministic seed")
	cmd.Flags().IntVar(&customers, "customers", 50, "synthetic customers, 6 cycles each")
	return cmd
}

func runPolicy(seed int64, customers int) error {
	result := harness.RunBatch(seed, customers)
	fmt.Printf("%s %s\n\n", muted("seed"), teal(fmt.Sprint(seed)))
	fmt.Printf("  %s   %s / %s recovered\n", bold("system  "), green(fmt.Sprint(result.SystemRecovered)), fmt.Sprint(result.Total))
	fmt.Printf("  %s   %s / %s recovered\n", bold("baseline"), amber(fmt.Sprint(result.BaselineRecovered)), fmt.Sprint(result.Total))
	fmt.Printf("  %s   %s / %s recovered\n", bold("oracle  "), teal(fmt.Sprint(result.OracleRecovered)), fmt.Sprint(result.Total))
	fmt.Println()
	fmt.Println(muted("For the real, 200-seed measured number with confidence intervals, use `quiescent report`."))
	return nil
}
