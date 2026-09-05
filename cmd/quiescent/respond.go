package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/spf13/cobra"
)

func newRespondCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "respond <cycleID> <yes|no>",
		Short: "Answer this cycle's pending balance-check trigger, standing in for the customer",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRespond(cmd.Context(), domain.CycleID(args[0]), args[1])
		},
	}
}

func runRespond(ctx context.Context, cycleID domain.CycleID, answer string) error {
	if answer != "yes" && answer != "no" {
		return fmt.Errorf("answer must be yes or no, got %q", answer)
	}

	s, err := store.Open(ctx, envString("DB_URL", ""))
	if err != nil {
		return err
	}
	defer s.Close()

	trig, err := s.PendingTriggerForCycle(ctx, cycleID)
	if err != nil {
		return err
	}
	if trig == nil {
		fmt.Println(amber(bold("no pending balance-check trigger")) + " for this cycle right now — nothing to answer")
		return nil
	}

	now := time.Now().UTC()
	if err := s.RespondTrigger(ctx, cycleID, trig.Seq, answer, now); err != nil {
		return err
	}

	inputs, _ := json.Marshal(map[string]any{"seq": trig.Seq, "sentAt": trig.SentAt, "expiresAt": trig.ExpiresAt})
	decision, _ := json.Marshal(map[string]any{"response": answer})
	_ = s.AppendAudit(ctx, domain.AuditEntry{
		CycleID:       cycleID,
		CorrelationID: domain.CorrelationID(trig.TriggerID),
		Event:         "balance_trigger_answered",
		Inputs:        inputs,
		Decision:      decision,
		Reason:        "customer answered the balance-check trigger",
	})

	label := green(bold("yes"))
	tail := "— the scheduler will notify and fire early, within 24 hours"
	if answer == "no" {
		label = red(bold("no"))
		tail = "— the normal scheduled retry will still happen regardless"
	}
	fmt.Printf("%s recorded %s for cycle %s, attempt #%d %s\n",
		green(bold("responded")), label, teal(string(cycleID)), trig.Seq, muted(tail))
	return nil
}
