package classify

import "github.com/VIM4L-M/Quiescent/internal/domain"

var table = map[domain.FailureCode]domain.FailureClass{
	domain.FailureInsufficientFunds:     domain.ClassRetryLater,
	domain.FailureMandateRevoked:        domain.ClassTerminal,
	domain.FailureAmountExceedsLimit:    domain.ClassTerminal,
	domain.FailurePreDebitNoticeMissing: domain.ClassRetryLater,

	"PSP_UNAVAILABLE":    domain.ClassRetryNow,
	"PAYER_AUTH_PENDING": domain.ClassRetryNow,
	"TECHNICAL_DECLINE":  domain.ClassRetryNow,
	"BANK_UNREACHABLE":   domain.ClassRetryNow,
	"ACCOUNT_FROZEN":     domain.ClassManualReview,
}

func Classify(code domain.FailureCode) (domain.FailureClass, bool) {
	class, ok := table[code]
	if !ok {
		return domain.ClassManualReview, false
	}
	return class, true
}
