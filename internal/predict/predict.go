package predict

import "github.com/VIM4L-M/Quiescent/internal/domain"

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
