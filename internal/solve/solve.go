package solve

import (
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/predict"
)

var offsets = [...]time.Duration{24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour}

const noticeLeadBuffer = 15 * time.Minute

type Plan struct {
	ScheduledFor time.Time
	Reason       domain.DecisionReason
}

func First(dueDate time.Time, preferredHour *int, now time.Time) Plan {
	candidate, hourBasis := applyPreferredHour(dueDate, preferredHour)
	candidate, noticeShift := ensureNoticeLead(candidate, now)
	candidate, windowShift := shiftOutOfBlockedWindow(candidate)

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
				BlockedWindowShift: windowShift,
				NoticeDeadline:     candidate.Add(-domain.NoticeLead).Format(time.RFC3339),
				NoticeLeadShift:    noticeShift,
				RailRules:          "original_attempt",
			},
			BudgetBefore: 0,
			BudgetAfter:  1,
		},
	}
}

func Next(dueDate time.Time, attemptsUsed int16, code domain.FailureCode, class domain.FailureClass,
	preferredHour *int, now time.Time) (Plan, bool) {

	if class == domain.ClassTerminal || class == domain.ClassManualReview {
		return Plan{}, false
	}

	slotIndex := int(attemptsUsed) - 1
	if slotIndex < 0 || slotIndex >= len(offsets) {
		return Plan{}, false
	}

	candidate, hourBasis := applyPreferredHour(dueDate.Add(offsets[slotIndex]), preferredHour)
	candidate, noticeShift := ensureNoticeLead(candidate, now)
	candidate, windowShift := shiftOutOfBlockedWindow(candidate)

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
				BlockedWindowShift: windowShift,
				NoticeDeadline:     candidate.Add(-domain.NoticeLead).Format(time.RFC3339),
				NoticeLeadShift:    noticeShift,
				RailRules:          "fixed_slot_" + slotName(slotIndex),
			},
			BudgetBefore: attemptsUsed,
			BudgetAfter:  attemptsUsed + 1,
		},
	}, true
}

func AfterConfirmation(respondedAt time.Time, code domain.FailureCode, attemptsUsed int16) Plan {
	candidate := respondedAt.Add(domain.NoticeLead + noticeLeadBuffer)
	candidate, windowShift := shiftOutOfBlockedWindow(candidate)

	return Plan{
		ScheduledFor: candidate,
		Reason: domain.DecisionReason{
			FailureCode:     code,
			Class:           domain.ClassRetryLater,
			ClassifiedBy:    domain.ClassifiedByTable,
			PredictedFunds:  "confirmed",
			PredictionBasis: "customer confirmed balance is available, via a balance-check trigger",
			Constraints: domain.ReasonConstraints{
				BlockedWindowShift: windowShift,
				NoticeDeadline:     candidate.Add(-domain.NoticeLead).Format(time.RFC3339),
				NoticeLeadShift:    "none",
				RailRules:          "trigger_confirmed",
			},
			BudgetBefore: attemptsUsed,
			BudgetAfter:  attemptsUsed + 1,
		},
	}
}

func applyPreferredHour(t time.Time, preferredHour *int) (time.Time, string) {
	if preferredHour == nil {
		return t, ""
	}
	local := t.In(domain.IST)
	adjusted := time.Date(local.Year(), local.Month(), local.Day(), *preferredHour, 0, 0, 0, domain.IST)
	return adjusted, "shifted to this customer's historically best hour on the same mandated day"
}

func ensureNoticeLead(candidate, now time.Time) (time.Time, string) {
	earliest := now.Add(domain.NoticeLead + noticeLeadBuffer)
	if !candidate.Before(earliest) {
		return candidate, "none"
	}
	return earliest, fmt.Sprintf(
		"the mandated slot %s didn't leave 24h to deliver the pre-debit notice from %s — pushed to %s",
		candidate.Format(time.RFC3339), now.Format(time.RFC3339), earliest.Format(time.RFC3339))
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
