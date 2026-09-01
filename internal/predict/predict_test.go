package predict_test

import (
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/predict"
)

func TestHorizonForEachClass(t *testing.T) {
	classes := []domain.FailureClass{
		domain.ClassRetryNow, domain.ClassRetryLater,
		domain.ClassTerminal, domain.ClassManualReview,
	}
	for _, c := range classes {
		funds, basis := predict.Horizon(c)
		if funds == "" || basis == "" {
			t.Errorf("%s: expected non-empty funds/basis, got %q / %q", c, funds, basis)
		}
	}
}

func TestHorizonDistinguishesFundsFromTransientFailures(t *testing.T) {
	fundsRelated, _ := predict.Horizon(domain.ClassRetryLater)
	transient, _ := predict.Horizon(domain.ClassRetryNow)
	if fundsRelated == transient {
		t.Fatal("a funds-related failure and a transient failure must not get the same prediction")
	}
}

func TestPreferredHourNeedsAtLeastTwoDataPoints(t *testing.T) {
	if hour, _ := predict.PreferredHour(nil); hour != nil {
		t.Fatalf("no history: want nil, got %v", *hour)
	}
	one := []time.Time{time.Date(2026, 3, 1, 14, 0, 0, 0, domain.IST)}
	if hour, _ := predict.PreferredHour(one); hour != nil {
		t.Fatalf("a single data point is not a pattern: want nil, got %v", *hour)
	}
}

func TestPreferredHourPicksTheMostCommonHour(t *testing.T) {
	history := []time.Time{
		time.Date(2026, 1, 5, 14, 12, 0, 0, domain.IST),
		time.Date(2026, 2, 5, 14, 45, 0, 0, domain.IST),
		time.Date(2026, 3, 5, 14, 3, 0, 0, domain.IST),
		time.Date(2026, 4, 5, 9, 0, 0, 0, domain.IST),
	}
	hour, basis := predict.PreferredHour(history)
	if hour == nil {
		t.Fatal("expected a preferred hour")
	}
	if *hour != 14 {
		t.Fatalf("hour: got %d want 14 (3 of 4 successes)", *hour)
	}
	if basis == "" {
		t.Fatal("expected a non-empty basis explaining the pattern")
	}
}

func TestPreferredHourBreaksTiesTowardTheEarlierHour(t *testing.T) {
	history := []time.Time{
		time.Date(2026, 1, 5, 9, 0, 0, 0, domain.IST),
		time.Date(2026, 2, 5, 20, 0, 0, 0, domain.IST),
	}
	hour, _ := predict.PreferredHour(history)
	if hour == nil || *hour != 9 {
		t.Fatalf("tie should break toward the earlier hour: got %v want 9", hour)
	}
}
