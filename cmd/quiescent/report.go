package main

import (
	"fmt"

	"github.com/VIM4L-M/Quiescent/internal/harness"
	"github.com/spf13/cobra"
)

func newReportCmd() *cobra.Command {
	var seeds int
	var customers int

	cmd := &cobra.Command{
		Use:   "report",
		Short: "The measured recovery numbers — system vs. baseline vs. oracle",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(seeds, customers)
		},
	}
	cmd.Flags().IntVar(&seeds, "seeds", 200, "number of seeds to run")
	cmd.Flags().IntVar(&customers, "customers", 50, "synthetic customers per seed, 6 cycles each")
	return cmd
}

func runReport(seedCount, customers int) error {
	seeds := make([]int64, seedCount)
	for i := range seeds {
		seeds[i] = int64(10_000 + i)
	}
	fmt.Printf("%s %s seeds, %s customers each...\n\n", muted("running"), teal(fmt.Sprint(seedCount)), teal(fmt.Sprint(customers)))

	report := harness.RunMany(seeds, customers)
	fmt.Printf("  %s   %s\n", bold("system  "), green(fmt.Sprintf("%.2f%%", report.SystemRate*100)))
	fmt.Printf("  %s   %s\n", bold("baseline"), amber(fmt.Sprintf("%.2f%%", report.BaselineRate*100)))
	fmt.Printf("  %s   %s\n", bold("oracle  "), teal(fmt.Sprintf("%.2f%%", report.OracleRate*100)))
	fmt.Println()
	fmt.Printf("  achievable lift   %s pp\n", fmt.Sprintf("%.2f", report.AchievableLift*100))
	fmt.Printf("  captured lift     %s pp (%s%%, +/- %.2fpp 95%% CI)\n",
		fmt.Sprintf("%.2f", report.CapturedLift*100), fmt.Sprintf("%.1f", report.CapturedPct), report.CapturedLiftCI95*100)
	return nil
}
