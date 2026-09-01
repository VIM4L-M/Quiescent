package harness

import (
	"math"

	"github.com/VIM4L-M/Quiescent/internal/provider"
)

type BatchResult struct {
	Seed              int64
	Total             int
	SystemRecovered   int
	BaselineRecovered int
	OracleRecovered   int
}

func RunBatch(seed int64, cycles []CycleSpec) BatchResult {
	w := provider.NewWorld(seed)
	res := BatchResult{Seed: seed, Total: len(cycles)}
	for _, c := range cycles {
		if SimulateSystem(w, seed, c).Recovered {
			res.SystemRecovered++
		}
		if SimulateBaseline(w, seed, c).Recovered {
			res.BaselineRecovered++
		}
		if SimulateOracle(w, seed, c).Recovered {
			res.OracleRecovered++
		}
	}
	return res
}

type Report struct {
	Seeds        int
	CyclesPerRun int

	SystemRate   float64
	BaselineRate float64
	OracleRate   float64

	AchievableLift float64 // oracle - baseline
	CapturedLift   float64 // system - baseline
	CapturedPct    float64 // capturedLift / achievableLift * 100

	CapturedLiftCI95 float64 // +/- half-width, paired across seeds
}

func RunMany(seeds []int64, cyclesPerSeed int) Report {
	batches := make([]BatchResult, len(seeds))
	for i, seed := range seeds {
		batches[i] = RunBatch(seed, GenerateCycles(seed, cyclesPerSeed))
	}
	return aggregate(batches)
}

func aggregate(batches []BatchResult) Report {
	var totalCycles, totalSystem, totalBaseline, totalOracle int
	pairedLift := make([]float64, len(batches))

	for i, b := range batches {
		totalCycles += b.Total
		totalSystem += b.SystemRecovered
		totalBaseline += b.BaselineRecovered
		totalOracle += b.OracleRecovered
		if b.Total > 0 {
			pairedLift[i] = float64(b.SystemRecovered-b.BaselineRecovered) / float64(b.Total)
		}
	}

	r := Report{
		Seeds:        len(batches),
		CyclesPerRun: totalCyclesPerRun(batches),
	}
	if totalCycles > 0 {
		r.SystemRate = float64(totalSystem) / float64(totalCycles)
		r.BaselineRate = float64(totalBaseline) / float64(totalCycles)
		r.OracleRate = float64(totalOracle) / float64(totalCycles)
	}
	r.AchievableLift = r.OracleRate - r.BaselineRate
	r.CapturedLift = r.SystemRate - r.BaselineRate
	if r.AchievableLift > 0 {
		r.CapturedPct = r.CapturedLift / r.AchievableLift * 100
	}

	if len(pairedLift) > 1 {
		mean, stderr := meanStderr(pairedLift)
		_ = mean
		r.CapturedLiftCI95 = 1.96 * stderr
	}
	return r
}

func totalCyclesPerRun(batches []BatchResult) int {
	if len(batches) == 0 {
		return 0
	}
	return batches[0].Total
}

func meanStderr(xs []float64) (mean, stderr float64) {
	n := float64(len(xs))
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	mean = sum / n
	varSum := 0.0
	for _, x := range xs {
		d := x - mean
		varSum += d * d
	}
	variance := varSum / (n - 1)
	stderr = math.Sqrt(variance / n)
	return mean, stderr
}
