package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func rootLogger() *slog.Logger {
	return slog.New(newColorLogHandler(os.Stdout))
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := &cobra.Command{
		Use:   "quiescent",
		Short: "A durable retry sequencer for failed mandate debits",
		Run: func(cmd *cobra.Command, args []string) {
			printBanner()
			_ = cmd.Help()
		},
	}
	root.AddCommand(
		newSchedulerCmd(),
		newWorkerCmd(),
		newIntelligenceCmd(),
		newProviderSimCmd(),
		newMigrateCmd(),
		newSeedCmd(),
		newCreateCmd(),
		newCycleCmd(),
		newRespondCmd(),
		newPolicyCmd(),
		newReportCmd(),
		newVerifyCmd(),
	)
	root.SetContext(ctx)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
