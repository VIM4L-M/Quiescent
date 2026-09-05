package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/execute"
	"github.com/VIM4L-M/Quiescent/internal/intelligence"
	"github.com/VIM4L-M/Quiescent/internal/outbox"
	"github.com/VIM4L-M/Quiescent/internal/provider"
	"github.com/VIM4L-M/Quiescent/internal/reconcile"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/time/rate"
)

func newWorkerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Execute debits — never decides anything",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorker(cmd.Context(), rootLogger())
		},
	}
}

func runWorker(ctx context.Context, log *slog.Logger) error {
	s, err := store.Open(ctx, envString("DB_URL", ""))
	if err != nil {
		return err
	}
	defer s.Close()

	bank := provider.NewClient(
		envString("PROVIDER_CLIENT_ADDR", "http://localhost:8081"),
		envDuration("PROVIDER_CLIENT_TIMEOUT", 5*time.Second))

	holder := envString("WORKER_ID", defaultWorkerID())
	interval := envDuration("WORKER_INTERVAL", 5*time.Second)
	batch := envInt("WORKER_BATCH", 20)

	worker := execute.New(s, bank, holder, log)
	worker.Classifier = intelligence.New(os.Getenv("GROQ_API_KEY"), envString("INTELLIGENCE_MODEL", "openai/gpt-oss-120b"))
	reconciler := reconcile.New(s, bank, holder, log)
	relay := outbox.New(s, outbox.LogSender{Log: log}, log)

	limiter := rate.NewLimiter(rate.Limit(envFloat("WORKER_RATE_LIMIT", 5)), envInt("WORKER_RATE_BURST", 5))
	jitterMax := envDuration("WORKER_JITTER_MAX", 500*time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		pollLoop(ctx, interval, func() { claimTick(ctx, s, worker, batch, limiter, jitterMax, log) })
	}()
	go func() {
		defer wg.Done()
		pollLoop(ctx, interval, func() { reconcileTick(ctx, s, reconciler, batch, log) })
	}()
	go func() {
		defer wg.Done()
		pollLoop(ctx, interval, func() { relayTick(ctx, relay, batch, log) })
	}()
	wg.Wait()
	return ctx.Err()
}

func pollLoop(ctx context.Context, interval time.Duration, tick func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

func claimTick(ctx context.Context, s *store.Store, w *execute.Worker, batch int,
	limiter *rate.Limiter, jitterMax time.Duration, log *slog.Logger) {

	attempts, err := s.DueAttempts(ctx, batch)
	if err != nil {
		log.Error("worker: due attempts", "error", err)
		return
	}
	for _, a := range attempts {
		if jitterMax > 0 {
			select {
			case <-time.After(time.Duration(rand.Int64N(int64(jitterMax)))):
			case <-ctx.Done():
				return
			}
		}
		if err := limiter.Wait(ctx); err != nil {
			return
		}

		result, err := w.FireOne(ctx, a)
		if err != nil {
			log.Error("worker: fire one", "attemptID", a.AttemptID, "error", err)
			continue
		}
		log.Info("worker: claim tick", "attemptID", a.AttemptID, "result", result)
	}
}

func reconcileTick(ctx context.Context, s *store.Store, r *reconcile.Reconciler, batch int, log *slog.Logger) {
	attempts, err := s.NeedsReconciliation(ctx, batch)
	if err != nil {
		log.Error("worker: needs reconciliation", "error", err)
		return
	}
	for _, a := range attempts {
		result, err := r.Resolve(ctx, a)
		if err != nil {
			log.Error("worker: reconcile", "attemptID", a.AttemptID, "error", err)
			continue
		}
		log.Info("worker: reconcile tick", "attemptID", a.AttemptID, "result", result)
	}
}

func relayTick(ctx context.Context, relay *outbox.Relay, batch int, log *slog.Logger) {
	results, err := relay.ProcessBatch(ctx, batch)
	if err != nil {
		log.Error("worker: process notices", "error", err)
		return
	}
	if len(results) > 0 {
		log.Info("worker: notice relay tick", "count", len(results))
	}
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "worker"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}
