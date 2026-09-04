package harness

import (
	"time"

	"github.com/VIM4L-M/Quiescent/internal/classify"
	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/predict"
	"github.com/VIM4L-M/Quiescent/internal/provider"
	"github.com/VIM4L-M/Quiescent/internal/solve"
)

type CycleSpec struct {
	CycleID     domain.CycleID
	CustomerID  domain.CustomerID
	Rail        domain.Rail
	AmountPaise int64
	DueDate     time.Time
}

type Outcome struct {
	Recovered    bool
	AttemptsUsed int
	FiredAt      time.Time
}

func decide(w *provider.World, seed int64, c CycleSpec, attemptNumber int, firedAt time.Time) provider.Decision {
	return provider.Decide(provider.Conditions{
		Seed:          seed,
		CycleID:       c.CycleID,
		AttemptNumber: attemptNumber,
		Rail:          c.Rail,
		AmountPaise:   c.AmountPaise,
		BalancePaise:  w.BalanceAt(c.CycleID, firedAt),
		FiredAt:       firedAt,
	})
}

func SimulateSystem(w *provider.World, seed int64, c CycleSpec, history []time.Time) Outcome {
	var attemptsUsed int16
	var lastCode domain.FailureCode
	preferredHour, _ := predict.PreferredHour(history)

	for attemptsUsed < int16(domain.MaxAttempts) {
		var plan solve.Plan
		if attemptsUsed == 0 {
			plan = solve.First(c.DueDate, preferredHour)
		} else {
			class, _ := classify.Classify(lastCode)
			p, ok := solve.Next(c.DueDate, attemptsUsed, lastCode, class, preferredHour)
			if !ok {
				return Outcome{Recovered: false, AttemptsUsed: int(attemptsUsed)}
			}
			plan = p
		}
		attemptsUsed++
		result := decide(w, seed, c, int(attemptsUsed), plan.ScheduledFor)
		if result.Outcome == domain.OutcomeSuccess {
			return Outcome{Recovered: true, AttemptsUsed: int(attemptsUsed), FiredAt: plan.ScheduledFor}
		}
		lastCode = result.FailureCode
	}
	return Outcome{Recovered: false, AttemptsUsed: int(attemptsUsed)}
}

func SimulateCustomerSequence(w *provider.World, seed int64, sequence []CycleSpec) []Outcome {
	outcomes := make([]Outcome, len(sequence))
	var history []time.Time
	for i, c := range sequence {
		outcome := SimulateSystem(w, seed, c, history)
		outcomes[i] = outcome
		if outcome.Recovered {
			history = append(history, outcome.FiredAt)
		}
	}
	return outcomes
}

func SimulateBaseline(w *provider.World, seed int64, c CycleSpec) Outcome {
	var attemptsUsed int16

	for attemptsUsed < int16(domain.MaxAttempts) {
		firedAt, ok := BaselineNext(c.DueDate, attemptsUsed)
		if !ok {
			return Outcome{Recovered: false, AttemptsUsed: int(attemptsUsed)}
		}
		attemptsUsed++
		result := decide(w, seed, c, int(attemptsUsed), firedAt)
		if result.Outcome == domain.OutcomeSuccess {
			return Outcome{Recovered: true, AttemptsUsed: int(attemptsUsed)}
		}
	}
	return Outcome{Recovered: false, AttemptsUsed: int(attemptsUsed)}
}

const (
	oracleSearchWindow = 7 * 24 * time.Hour
	oracleSearchStep   = time.Hour
)

func BestSlot(w *provider.World, seed int64, c CycleSpec, attemptNumber int) (time.Time, float64) {
	best := c.DueDate
	bestP := -1.0
	end := c.DueDate.Add(oracleSearchWindow)
	for t := c.DueDate; t.Before(end); t = t.Add(oracleSearchStep) {
		if domain.Blocked(t) {
			continue
		}
		p := provider.SuccessProbability(provider.Conditions{
			Seed:          seed,
			CycleID:       c.CycleID,
			AttemptNumber: attemptNumber,
			Rail:          c.Rail,
			AmountPaise:   c.AmountPaise,
			BalancePaise:  w.BalanceAt(c.CycleID, t),
			FiredAt:       t,
		})
		if p > bestP {
			bestP = p
			best = t
		}
	}
	return best, bestP
}

func SimulateOracle(w *provider.World, seed int64, c CycleSpec) Outcome {
	var attemptsUsed int16

	for attemptsUsed < int16(domain.MaxAttempts) {
		attemptsUsed++
		firedAt, _ := BestSlot(w, seed, c, int(attemptsUsed))
		result := decide(w, seed, c, int(attemptsUsed), firedAt)
		if result.Outcome == domain.OutcomeSuccess {
			return Outcome{Recovered: true, AttemptsUsed: int(attemptsUsed)}
		}
	}
	return Outcome{Recovered: false, AttemptsUsed: int(attemptsUsed)}
}
