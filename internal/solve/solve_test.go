package solve_test

import (
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/solve"
)

func TestNextProducesTheThreeFixedOffsets(t *testing.T) {
	due := time.Date(2026, 3, 5, 8, 0, 0, 0, time.UTC) // 08:00 UTC = 13:30 IST, outside both blocked windows
	want := []time.Duration{24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour}

	for i, attemptsUsed := range []int16{1, 2, 3} {
		plan, ok := solve.Next(due, attemptsUsed, domain.FailureInsufficientFunds, domain.ClassRetryLater, nil)
		if !ok {
			t.Fatalf("attemptsUsed=%d: expected a plan", attemptsUsed)
		}
		got := plan.ScheduledFor.Sub(due)
		if got != want[i] {
			t.Fatalf("attemptsUsed=%d: offset got %s want %s", attemptsUsed, got, want[i])
		}
	}
}

func TestNextRejectsExhaustedOrOutOfRangeBudget(t *testing.T) {
	due := time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	for _, attemptsUsed := range []int16{0, 4, 5} {
		if _, ok := solve.Next(due, attemptsUsed, domain.FailureInsufficientFunds, domain.ClassRetryLater, nil); ok {
			t.Fatalf("attemptsUsed=%d: expected no plan — out of the 1..3 retry range", attemptsUsed)
		}
	}
}

func TestNextRejectsTerminalAndManualReview(t *testing.T) {
	due := time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	for _, class := range []domain.FailureClass{domain.ClassTerminal, domain.ClassManualReview} {
		if _, ok := solve.Next(due, 1, domain.FailureMandateRevoked, class, nil); ok {
			t.Fatalf("class=%s: a non-retryable failure must never get a schedule", class)
		}
	}
}

func TestNextNeverSchedulesIntoTheBlockedWindow(t *testing.T) {
	for day := 0; day < 30; day++ {
		for hour := 0; hour < 24; hour++ {
			due := time.Date(2026, 3, 1+day, hour, 0, 0, 0, time.UTC)
			for _, attemptsUsed := range []int16{1, 2, 3} {
				plan, ok := solve.Next(due, attemptsUsed, domain.FailureInsufficientFunds, domain.ClassRetryLater, nil)
				if !ok {
					t.Fatalf("due=%s attemptsUsed=%d: expected a plan", due, attemptsUsed)
				}
				if domain.Blocked(plan.ScheduledFor) {
					t.Fatalf("due=%s attemptsUsed=%d: scheduled %s falls inside the blocked window",
						due, attemptsUsed, plan.ScheduledFor)
				}
			}
		}
	}
}

func TestNextNoticeDeadlineIsExactlyOneDayBeforeFiring(t *testing.T) {
	due := time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	plan, ok := solve.Next(due, 1, domain.FailureInsufficientFunds, domain.ClassRetryLater, nil)
	if !ok {
		t.Fatal("expected a plan")
	}
	deadline, err := time.Parse(time.RFC3339, plan.Reason.Constraints.NoticeDeadline)
	if err != nil {
		t.Fatalf("noticeDeadline not RFC3339: %v", err)
	}
	if !deadline.Equal(plan.ScheduledFor.Add(-24 * time.Hour)) {
		t.Fatalf("noticeDeadline: got %s want exactly 24h before %s", deadline, plan.ScheduledFor)
	}
}

func TestNextDecisionReasonIsValidForInsertAttempt(t *testing.T) {
	due := time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	plan, ok := solve.Next(due, 2, domain.FailureInsufficientFunds, domain.ClassRetryLater, nil)
	if !ok {
		t.Fatal("expected a plan")
	}
	r := plan.Reason
	if !r.Class.Valid() {
		t.Errorf("class %q is not valid", r.Class)
	}
	if !r.ClassifiedBy.Valid() {
		t.Errorf("classifiedBy %q is not valid", r.ClassifiedBy)
	}
	if r.Confidence != nil {
		t.Error("a table classification must not carry a confidence score")
	}
	if r.BudgetBefore != 2 || r.BudgetAfter != 3 {
		t.Errorf("budget: got before=%d after=%d want 2/3", r.BudgetBefore, r.BudgetAfter)
	}
}

func TestPreferredHourChangesTimeButNeverTheMandatedDay(t *testing.T) {
	due := time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	hour := 16 // 16:00 IST — outside the blocked window
	plan, ok := solve.Next(due, 1, domain.FailureInsufficientFunds, domain.ClassRetryLater, &hour)
	if !ok {
		t.Fatal("expected a plan")
	}

	wantDay := due.Add(24 * time.Hour).In(domain.IST)
	got := plan.ScheduledFor.In(domain.IST)
	if got.Year() != wantDay.Year() || got.YearDay() != wantDay.YearDay() {
		t.Fatalf("preferred hour must not change the mandated day: got %s want same day as %s", got, wantDay)
	}
	if got.Hour() != hour {
		t.Fatalf("hour: got %d want %d", got.Hour(), hour)
	}
}

func TestPreferredHourInsideBlockedWindowStillGetsShifted(t *testing.T) {
	due := time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	hour := 11 // 11:00 IST — inside the 10:00-13:00 blocked window
	plan, ok := solve.Next(due, 1, domain.FailureInsufficientFunds, domain.ClassRetryLater, &hour)
	if !ok {
		t.Fatal("expected a plan")
	}
	if domain.Blocked(plan.ScheduledFor) {
		t.Fatalf("a preferred hour inside the blocked window must still be shifted out, got %s", plan.ScheduledFor)
	}
}
