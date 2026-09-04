package harness_test

import (
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/harness"
)

func TestReport200Seeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the full 200-seed measurement run in -short mode")
	}
	seeds := make([]int64, 200)
	for i := range seeds {
		seeds[i] = int64(10_000 + i)
	}
	report := harness.RunMany(seeds, 50)
	t.Logf("seeds=%d customersPerSeed=%d cyclesPerRun=%d", report.Seeds, 50, report.CyclesPerRun)
	t.Logf("system=%.4f baseline=%.4f oracle=%.4f", report.SystemRate, report.BaselineRate, report.OracleRate)
	t.Logf("achievableLift=%.4f capturedLift=%.4f capturedPct=%.2f%% (+/- %.4f, 95%% CI)",
		report.AchievableLift, report.CapturedLift, report.CapturedPct, report.CapturedLiftCI95)
}
