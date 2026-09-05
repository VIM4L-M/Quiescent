package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/schedule"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/spf13/cobra"
)

func newSchedulerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scheduler",
		Short: "Decide what to retry and when — never fires a debit",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScheduler(cmd.Context(), rootLogger())
		},
	}
}

func runScheduler(ctx context.Context, log *slog.Logger) error {
	s, err := store.Open(ctx, envString("DB_URL", ""))
	if err != nil {
		return err
	}
	defer s.Close()

	sched := schedule.New(s, log)
	interval := envDuration("SCHEDULER_INTERVAL", 5*time.Second)
	batch := envInt("SCHEDULER_BATCH", 50)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			scheduleTick(ctx, s, sched, batch, log)
		}
	}
}

func scheduleTick(ctx context.Context, s *store.Store, sched *schedule.Scheduler, batch int, log *slog.Logger) {
	cycles, err := s.SchedulableCycles(ctx, batch)
	if err != nil {
		log.Error("scheduler: list schedulable cycles", "error", err)
		return
	}
	for _, c := range cycles {
		lastCode, err := lastFailureCode(ctx, s, c)
		if err != nil {
			log.Error("scheduler: load last failure code", "cycleID", c.CycleID, "error", err)
			continue
		}
		result, err := sched.ScheduleNext(ctx, c, lastCode)
		if err != nil {
			log.Error("scheduler: schedule next", "cycleID", c.CycleID, "error", err)
			continue
		}
		log.Info("scheduler: tick", "cycleID", c.CycleID, "result", result)
	}
}

func lastFailureCode(ctx context.Context, s *store.Store, c domain.MandateCycle) (domain.FailureCode, error) {
	if c.AttemptsUsed == 0 {
		return "", nil
	}
	attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
	if err != nil {
		return "", err
	}
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].FailureCode != nil {
			return *attempts[i].FailureCode, nil
		}
	}
	return "", nil
}
