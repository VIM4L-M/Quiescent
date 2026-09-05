package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/VIM4L-M/Quiescent/internal/provider"
	"github.com/spf13/cobra"
)

func newProviderSimCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "provider-sim",
		Short: "The simulated bank, for testing and demos",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProviderSim(cmd.Context(), rootLogger())
		},
	}
}

func runProviderSim(ctx context.Context, log *slog.Logger) error {
	cfg := provider.Config{
		Seed:        envInt64("PROVIDER_SEED", 1),
		Addr:        envString("PROVIDER_ADDR", ":8081"),
		OracleAddr:  envString("PROVIDER_ORACLE_ADDR", ":8082"),
		LedgerPath:  envString("PROVIDER_LEDGER_PATH", "ledger.jsonl"),
		FixturePath: os.Getenv("PROVIDER_FIXTURE_PATH"),
	}
	sim, err := provider.New(cfg, log)
	if err != nil {
		return err
	}
	defer sim.Close()
	return sim.Run(ctx)
}
