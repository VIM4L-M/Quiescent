package harness_test

import (
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/harness"
	"github.com/VIM4L-M/Quiescent/internal/provider"
)

func TestBaselineNextUsesTheThreeFixedOffsetsOnly(t *testing.T) {
	due := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	want := []time.Duration{0, 24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour}
	for i, attemptsUsed := range []int16{0, 1, 2, 3} {
		got, ok := harness.BaselineNext(due, attemptsUsed)
		if !ok {
			t.Fatalf("attemptsUsed=%d: expected a slot", attemptsUsed)
		}
		if got.Sub(due) != want[i] {
			t.Fatalf("attemptsUsed=%d: offset got %s want %s", attemptsUsed, got.Sub(due), want[i])
		}
	}
	if _, ok := harness.BaselineNext(due, 4); ok {
		t.Fatal("attemptsUsed=4: baseline has exhausted its budget too, expected no slot")
	}
}

func TestSimulationsAreDeterministicUnderTheSameSeed(t *testing.T) {
	seed := int64(7)
	cycles := harness.GenerateCycles(seed, 20)
	w1 := provider.NewWorld(seed)
	w2 := provider.NewWorld(seed)

	for _, c := range cycles {
		a := harness.SimulateSystem(w1, seed, c)
		b := harness.SimulateSystem(w2, seed, c)
		if a != b {
			t.Fatalf("cycle %s: same seed produced different outcomes: %+v vs %+v", c.CycleID, a, b)
		}
	}
}

func TestOracleNeverRecoversFewerCyclesThanSystem(t *testing.T) {
	seed := int64(99)
	cycles := harness.GenerateCycles(seed, 200)
	w := provider.NewWorld(seed)

	for _, c := range cycles {
		sys := harness.SimulateSystem(w, seed, c)
		oracle := harness.SimulateOracle(w, seed, c)
		if sys.Recovered && !oracle.Recovered {
			t.Fatalf("cycle %s: system recovered but the oracle — which always fires at the single best "+
				"possible moment for the same underlying draw — did not; the oracle is supposed to be the "+
				"upper bound", c.CycleID)
		}
	}
}

func TestRunManyReportsNonNegativeAchievableLift(t *testing.T) {
	seeds := make([]int64, 30)
	for i := range seeds {
		seeds[i] = int64(1000 + i)
	}
	report := harness.RunMany(seeds, 50)

	if report.OracleRate < report.SystemRate {
		t.Fatalf("oracle rate (%v) must never be below the system rate (%v)", report.OracleRate, report.SystemRate)
	}
	if report.OracleRate < report.BaselineRate {
		t.Fatalf("oracle rate (%v) must never be below the baseline rate (%v)", report.OracleRate, report.BaselineRate)
	}
	if report.AchievableLift < 0 {
		t.Fatalf("achievable lift must be non-negative, got %v", report.AchievableLift)
	}
	t.Logf("system=%.3f baseline=%.3f oracle=%.3f capturedPct=%.1f%% (+/- %.3f)",
		report.SystemRate, report.BaselineRate, report.OracleRate, report.CapturedPct, report.CapturedLiftCI95)
}
