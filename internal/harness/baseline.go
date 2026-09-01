package harness

import "time"

var baselineOffsets = [...]time.Duration{24 * time.Hour, 72 * time.Hour, 7 * 24 * time.Hour}

func BaselineNext(dueDate time.Time, attemptsUsed int16) (time.Time, bool) {
	if attemptsUsed == 0 {
		return dueDate, true
	}
	i := int(attemptsUsed) - 1
	if i < 0 || i >= len(baselineOffsets) {
		return time.Time{}, false
	}
	return dueDate.Add(baselineOffsets[i]), true
}
