package solve

import (
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/predict"
)

var offsets = [...]time.Duration{24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour}

type Plan struct {
	ScheduledFor time.Time
	Reason       domain.DecisionReason
}

func First(dueDate time.Time, preferredHour *int) Plan {
	candidate, hourBasis := applyPreferredHour(dueDate, preferredHour)
	candidate, shift := shiftOutOfBlockedWindow(candidate)

	basis := "original attempt: no prior failure to learn from"
	if hourBasis != "" {
		basis = hourBasis
	}
	return Plan{
		ScheduledFor: candidate,
		Reason: domain.DecisionReason{
			Class:           domain.ClassRetryNow,
			ClassifiedBy:    domain.ClassifiedByTable,
			PredictedFunds:  "unknown",
			PredictionBasis: basis,
			Constraints: domain.ReasonConstraints{
				BlockedWindowShift: shift,
				NoticeDeadline:     candidate.Add(-domain.NoticeLead).Format(time.RFC3339),
				RailRules:          "original_attempt",
			},
			BudgetBefore: 0,
			BudgetAfter:  1,
		},
	}
}

func Next(dueDate time.Time, attemptsUsed int16, code domain.FailureCode, class domain.FailureClass, preferredHour *int) (Plan, bool) {
	if class == domain.ClassTerminal || class == domain.ClassManualReview {
		return Plan{}, false
	}

	slotIndex := int(attemptsUsed) - 1
	if slotIndex < 0 || slotIndex >= len(offsets) {
		return Plan{}, false
	}

	candidate, hourBasis := applyPreferredHour(dueDate.Add(offsets[slotIndex]), preferredHour)
	candidate, shift := shiftOutOfBlockedWindow(candidate)

	funds, basis := predict.Horizon(class)
	if hourBasis != "" {
		basis = hourBasis
	}

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
				NoticeDeadline:     candidate.Add(-domain.NoticeLead).Format(time.RFC3339),
				RailRules:          "fixed_slot_" + slotName(slotIndex),
			},
			BudgetBefore: attemptsUsed,
			BudgetAfter:  attemptsUsed + 1,
		},
	}, true
}

func applyPreferredHour(t time.Time, preferredHour *int) (time.Time, string) {
	if preferredHour == nil {
		return t, ""
	}
	local := t.In(domain.IST)
	adjusted := time.Date(local.Year(), local.Month(), local.Day(), *preferredHour, 0, 0, 0, domain.IST)
	return adjusted, "shifted to this customer's historically best hour on the same mandated day"
}

func shiftOutOfBlockedWindow(t time.Time) (time.Time, string) {
	label := domain.WindowLabel(t)
	if label == "" {
		return t, "none"
	}
	return domain.ShiftPastBlockedWindow(t), "shifted past the " + label + " IST blocked window"
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
