package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/store"
	"github.com/spf13/cobra"
)

func newCycleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cycle <cycleID>",
		Short: "Show one mandate's full attempt and audit history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCycle(cmd.Context(), domain.CycleID(args[0]))
		},
	}
}

func runCycle(ctx context.Context, cycleID domain.CycleID) error {
	s, err := store.Open(ctx, envString("DB_URL", ""))
	if err != nil {
		return err
	}
	defer s.Close()

	c, err := s.Cycle(ctx, cycleID)
	if err != nil {
		return err
	}
	fmt.Printf("%s  %s  attemptsUsed=%s/%d  rail=%s  amount=%s paise\n",
		teal(bold(string(c.CycleID))), stateLabel(c.State),
		amber(fmt.Sprint(c.AttemptsUsed)), domain.MaxAttempts, c.Rail, fmt.Sprint(c.AmountPaise))

	attempts, err := s.AttemptsByCycle(ctx, cycleID)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(bold("attempts:"))
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, a := range attempts {
		outcome := muted("pending")
		if a.Outcome != nil {
			outcome = string(*a.Outcome)
			if a.FailureCode != nil {
				outcome += " (" + string(*a.FailureCode) + ")"
			}
		}
		fired := muted("not yet fired")
		if a.FiredAt != nil {
			fired = a.FiredAt.Format("2006-01-02T15:04Z")
		}
		fmt.Fprintf(tw, "  #%d\tscheduled %s\tfired %s\toutcome %s\n",
			a.Seq, a.ScheduledFor.Format("2006-01-02T15:04Z"), fired, outcome)
	}
	tw.Flush()

	entries, err := s.AuditByCycle(ctx, cycleID)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		fmt.Println()
		fmt.Println(bold("audit:"))
		tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		for _, e := range entries {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", teal(e.Event), e.At.Format("2006-01-02T15:04Z"), muted(e.Reason))
		}
		tw.Flush()
	}
	return nil
}

func stateLabel(s domain.State) string {
	switch s {
	case domain.StateRecovered:
		return green(string(s))
	case domain.StateEscalated, domain.StateAbandoned:
		return red(string(s))
	case domain.StateHeld, domain.StateUnknown:
		return amber(string(s))
	default:
		return string(s)
	}
}
