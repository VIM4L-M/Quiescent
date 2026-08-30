package solve

import (
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/predict"
)

var offsets = [...]time.Duration{24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour}

const noticeLead = 24 * time.Hour

type Plan struct {
	ScheduledFor time.Time
	Reason       domain.DecisionReason
}

func Next(dueDate time.Time, attemptsUsed int16, code domain.FailureCode, class domain.FailureClass) (Plan, bool) {
	if class == domain.ClassTerminal || class == domain.ClassManualReview {
		return Plan{}, false
	}

	slotIndex := int(attemptsUsed) - 1
	if slotIndex < 0 || slotIndex >= len(offsets) {
		return Plan{}, false
	}

	candidate := dueDate.Add(offsets[slotIndex])
	shift := "none"
	if domain.Blocked(candidate) {
		local := candidate.In(domain.IST)
		candidate = time.Date(local.Year(), local.Month(), local.Day(), 13, 5, 0, 0, domain.IST)
		shift = "shifted past the 10:00-13:00 IST blocked window"
	}

	funds, basis := predict.Horizon(class)

	return Plan{
		ScheduledFor: candidate,
		Reason: domain.DecisionReason{
			FailureCode:     code,
			Class:           class,
			ClassifiedBy:    domain.ClassifiedByTable,
			PredictedFunds:  funds,
			PredictionBasis: basis,
			Constraints: domain.ReasonConstraints{
				BlockedWindowShift: shift,
				NoticeDeadline:     candidate.Add(-noticeLead).Format(time.RFC3339),
				RailRules:          "fixed_slot_" + slotName(slotIndex),
			},
			BudgetBefore: attemptsUsed,
			BudgetAfter:  attemptsUsed + 1,
		},
	}, true
}

func slotName(i int) string {
	switch i {
	case 0:
		return "t_plus_24h"
	case 1:
		return "t_plus_72h"
	default:
		return "t_plus_7d"
	}
}
