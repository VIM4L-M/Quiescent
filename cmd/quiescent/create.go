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

func newCreateCmd() *cobra.Command {
	var rail string
	var amountPaise int64
	var dueDate string
	var fireIn time.Duration

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Add one real mandate cycle by hand",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd.Context(), rail, amountPaise, dueDate, fireIn)
		},
	}
	cmd.Flags().StringVar(&rail, "rail", "upi_autopay", "upi_autopay or enach")
	cmd.Flags().Int64Var(&amountPaise, "amount", 50_000, "amount in paise")
	cmd.Flags().StringVar(&dueDate, "due", time.Now().UTC().Format("2006-01-02"), "due date, YYYY-MM-DD (ignored if --fire-in is set)")
	cmd.Flags().DurationVar(&fireIn, "fire-in", 0,
		"demo-only: skip the scheduler and fire at exactly now+duration (e.g. 2m). "+
			"The 24h pre-debit notice is still honored — simulated as delivered a day ago, not skipped.")
	return cmd
}

func runCreate(ctx context.Context, rail string, amountPaise int64, dueDate string, fireIn time.Duration) error {
	r := domain.Rail(rail)
	if !r.Valid() {
		return fmt.Errorf("--rail must be upi_autopay or enach, got %q", rail)
	}
	if amountPaise <= 0 {
		return fmt.Errorf("--amount must be positive")
	}

	s, err := store.Open(ctx, envString("DB_URL", ""))
	if err != nil {
		return err
	}
	defer s.Close()

	if fireIn > 0 {
		return runCreateFireIn(ctx, s, r, amountPaise, fireIn)
	}

	due, err := time.Parse("2006-01-02", dueDate)
	if err != nil {
		return fmt.Errorf("--due must be YYYY-MM-DD: %w", err)
	}
	c := domain.MandateCycle{
		CycleID:      domain.NewCycleID(),
		MandateID:    domain.NewMandateID(),
		CustomerID:   domain.NewCustomerID(),
		Rail:         r,
		AmountPaise:  amountPaise,
		DueDate:      due,
		AttemptsUsed: 0,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		return err
	}
	fmt.Printf("%s cycle %s (customer %s) — due %s, the scheduler will pick the exact hour\n",
		green(bold("created")), teal(string(c.CycleID)), muted(string(c.CustomerID)), due.Format("2006-01-02"))
	return nil
}

func runCreateFireIn(ctx context.Context, s *store.Store, r domain.Rail, amountPaise int64, fireIn time.Duration) error {
	now := time.Now().UTC()
	scheduledFor := now.Add(fireIn)
	deliverBy := scheduledFor.Add(-domain.NoticeLead)

	c := domain.MandateCycle{
		CycleID:      domain.NewCycleID(),
		MandateID:    domain.NewMandateID(),
		CustomerID:   domain.NewCustomerID(),
		Rail:         r,
		AmountPaise:  amountPaise,
		DueDate:      now,
		AttemptsUsed: 0,
		State:        domain.StatePending,
	}
	if err := s.CreateCycle(ctx, c); err != nil {
		return err
	}

	attempt := domain.Attempt{
		AttemptID:    domain.NewAttemptID(),
		CycleID:      c.CycleID,
		ScheduledFor: scheduledFor,
		DecisionReason: domain.DecisionReason{
			Class:           domain.ClassRetryLater,
			ClassifiedBy:    domain.ClassifiedByTable,
			PredictedFunds:  "n/a",
			PredictionBasis: "manual demo fire time via --fire-in",
			Constraints: domain.ReasonConstraints{
				BlockedWindowShift: "n/a",
				NoticeDeadline:     deliverBy.Format(time.RFC3339),
				RailRules:          string(r),
			},
			BudgetBefore: 0,
			BudgetAfter:  1,
		},
	}
	payload, err := json.Marshal(map[string]any{"amountPaise": amountPaise, "rail": r})
	if err != nil {
		return err
	}
	attempt, err = s.ReserveAttempt(ctx, c.CycleID, 0, attempt, payload, deliverBy)
	if err != nil {
		return err
	}
	if err := s.MarkNoticeDeliveredAt(ctx, attempt.AttemptID, domain.OutboxPreDebitNotice, deliverBy); err != nil {
		return err
	}

	fmt.Printf("%s cycle %s (customer %s)\n", green(bold("created")), teal(string(c.CycleID)), muted(string(c.CustomerID)))
	fmt.Printf("%s will fire at %s (in %s) — notice simulated as already delivered on time\n",
		amber(bold("scheduled")), teal(scheduledFor.Format(time.RFC3339)), muted(fireIn.String()))
	return nil
}
