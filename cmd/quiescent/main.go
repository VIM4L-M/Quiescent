package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/classify"
	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/execute"
	"github.com/VIM4L-M/Quiescent/internal/intelligence"
	"github.com/VIM4L-M/Quiescent/internal/outbox"
	"github.com/VIM4L-M/Quiescent/internal/provider"
	"github.com/VIM4L-M/Quiescent/internal/reconcile"
	"github.com/VIM4L-M/Quiescent/internal/schedule"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/VIM4L-M/Quiescent/internal/verify"
	"golang.org/x/time/rate"
)

func main() {
	role := flag.String("role", "", "scheduler | worker | intelligence | provider-sim | verify")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch *role {
	case "scheduler":
		err = runScheduler(ctx, log)
	case "worker":
		err = runWorker(ctx, log)
	case "intelligence":
		err = runIntelligence(ctx, log)
	case "provider-sim":
		err = runProviderSim(ctx, log)
	case "verify":
		err = runVerify(ctx, log)
	default:
		err = fmt.Errorf("unknown --role %q; want scheduler, worker, intelligence, provider-sim, or verify", *role)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("exited with error", "role", *role, "error", err)
		os.Exit(1)
	}
}

func runVerify(ctx context.Context, _ *slog.Logger) error {
	dbURL := envString("DB_URL", "")
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer s.Close()

	fmt.Printf("Running 6 invariants against %s...\n\n", dbURL)
	results, err := verify.Run(ctx, s)
	if err != nil {
		return err
	}

	failed := 0
	for _, r := range results {
		if r.Passed() {
			fmt.Printf("  [PASS] %d. %s\n", r.Number, r.Name)
			continue
		}
		failed++
		fmt.Printf("  [FAIL] %d. %s (%d row(s): %s)\n", r.Number, r.Name, len(r.Violations), strings.Join(r.Violations, ", "))
	}
	fmt.Printf("\n%d/%d passed", len(results)-failed, len(results))
	if failed > 0 {
		fmt.Printf(", %d failed\n", failed)
		return fmt.Errorf("%d invariant(s) failed", failed)
	}
	fmt.Println()
	return nil
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

func runIntelligence(ctx context.Context, log *slog.Logger) error {
	s, err := store.Open(ctx, envString("DB_URL", ""))
	if err != nil {
		return err
	}
	defer s.Close()

	narrator := intelligence.New(os.Getenv("GROQ_API_KEY"), envString("INTELLIGENCE_MODEL", "openai/gpt-oss-120b"))
	interval := envDuration("INTELLIGENCE_INTERVAL", 10*time.Second)
	batch := envInt("INTELLIGENCE_BATCH", 20)
	minOccurrences := envInt64("INTELLIGENCE_MIN_OCCURRENCES", 5)
	seen := map[domain.CycleID]bool{}
	advised := map[domain.FailureCode]bool{}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			narrateTick(ctx, s, narrator, batch, seen, log)
			adviseTick(ctx, s, narrator, minOccurrences, batch, advised, log)
		}
	}
}

func adviseTick(ctx context.Context, s *store.Store, adv intelligence.Advisor, minOccurrences int64, limit int,
	advised map[domain.FailureCode]bool, log *slog.Logger) {

	stats, err := s.FailureCodeStats(ctx, minOccurrences, limit)
	if err != nil {
		log.Error("intelligence: failure code stats", "error", err)
		return
	}
	for _, stat := range stats {
		if advised[stat.Code] {
			continue
		}
		if _, mapped := classify.Classify(stat.Code); mapped {
			continue
		}
		proposal, err := adv.Advise(ctx, stat)
		if err != nil {
			log.Error("intelligence: advise", "failureCode", stat.Code, "error", err)
			continue
		}
		advised[stat.Code] = true
		log.Warn("intelligence: policy proposal — human review required, not applied",
			"failureCode", stat.Code, "occurrences", stat.Occurrences,
			"recovered", stat.Recovered, "terminal", stat.Terminal,
			"proposedClass", proposal.Class, "confidence", proposal.Confidence, "rationale", proposal.Rationale)
	}
}

func narrateTick(ctx context.Context, s *store.Store, n intelligence.Narrator, batch int,
	seen map[domain.CycleID]bool, log *slog.Logger) {

	for _, state := range []domain.State{domain.StateRecovered, domain.StateEscalated, domain.StateAbandoned} {
		cycles, err := s.CyclesByState(ctx, state, batch)
		if err != nil {
			log.Error("intelligence: list cycles", "state", state, "error", err)
			continue
		}
		for _, c := range cycles {
			if seen[c.CycleID] {
				continue
			}
			attempts, err := s.AttemptsByCycle(ctx, c.CycleID)
			if err != nil {
				log.Error("intelligence: load attempts", "cycleID", c.CycleID, "error", err)
				continue
			}
			if len(attempts) == 0 {
				continue
			}
			text, err := n.Narrate(ctx, attempts)
			if err != nil {
				log.Error("intelligence: narrate", "cycleID", c.CycleID, "error", err)
				continue
			}
			seen[c.CycleID] = true
			log.Info("intelligence: narrated cycle", "cycleID", c.CycleID, "state", state, "narrative", text)
		}
	}
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envInt(key string, fallback int) int {
	return int(envInt64(key, int64(fallback)))
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}
