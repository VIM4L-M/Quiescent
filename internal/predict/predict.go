package predict

import (
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

const minHistoryForSignal = 2

func PreferredHour(history []time.Time) (hour *int, basis string) {
	if len(history) < minHistoryForSignal {
		return nil, fmt.Sprintf("only %d prior success(es) on record — not enough to trust a pattern", len(history))
	}

	counts := make(map[int]int, 24)
	for _, t := range history {
		counts[t.In(domain.IST).Hour()]++
	}

	bestHour, bestCount := 0, -1
	for h := 0; h < 24; h++ {
		if counts[h] > bestCount {
			bestHour, bestCount = h, counts[h]
		}
	}

	h := bestHour
	return &h, fmt.Sprintf("%d of %d past successes fired around %02d:00 IST", bestCount, len(history), bestHour)
}

func Horizon(class domain.FailureClass) (funds string, basis string) {
	switch class {
	case domain.ClassRetryLater:
		return "later_in_the_cycle", "funds-related failure: balance more likely to recover with more time"
	case domain.ClassRetryNow:
		return "unrelated_to_funds", "transient or technical failure: not a balance problem, retry as soon as allowed"
	default:
		return "unknown", "no funds signal for this failure class"
	}
}
