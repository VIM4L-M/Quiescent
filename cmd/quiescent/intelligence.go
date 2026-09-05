package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/classify"
	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/intelligence"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/spf13/cobra"
)

func newIntelligenceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "intelligence",
		Short: "The AI advisor (narrate, classify, propose) — cannot write anything",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIntelligence(cmd.Context(), rootLogger())
		},
	}
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
