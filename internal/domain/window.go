package domain

import (
	"time"

	_ "time/tzdata"
)

var IST = mustLoadIST()

func mustLoadIST() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		panic(err)
	}
	return loc
}

type blockedWindow struct {
	label      string
	fromMinute int
	toMinute   int
}

var blockedWindows = [...]blockedWindow{
	{"10:00-13:00", 10 * 60, 13 * 60},
	{"17:00-21:30", 17 * 60, 21*60 + 30},
}

const NoticeLead = 24 * time.Hour

func Blocked(t time.Time) bool {
	return findBlockedWindow(minuteOfDay(t)) != nil
}

func WindowLabel(t time.Time) string {
	w := findBlockedWindow(minuteOfDay(t))
	if w == nil {
		return ""
	}
	return w.label
}

func ShiftPastBlockedWindow(t time.Time) time.Time {
	local := t.In(IST)
	w := findBlockedWindow(minuteOfDay(t))
	if w == nil {
		return t
	}
	shiftedMinute := w.toMinute + 5
	return time.Date(local.Year(), local.Month(), local.Day(),
		shiftedMinute/60, shiftedMinute%60, 0, 0, IST)
}

func findBlockedWindow(m int) *blockedWindow {
	for i := range blockedWindows {
		if m >= blockedWindows[i].fromMinute && m <= blockedWindows[i].toMinute {
			return &blockedWindows[i]
		}
	}
	return nil
}

func minuteOfDay(t time.Time) int {
	local := t.In(IST)
	return local.Hour()*60 + local.Minute()
}
