package predict_test

import (
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/VIM4L-M/Quiescent/internal/predict"
)

func TestHorizonForEachClass(t *testing.T) {
	classes := []domain.FailureClass{
		domain.ClassRetryNow, domain.ClassRetryLater,
		domain.ClassTerminal, domain.ClassManualReview,
	}
	for _, c := range classes {
		funds, basis := predict.Horizon(c)
		if funds == "" || basis == "" {
			t.Errorf("%s: expected non-empty funds/basis, got %q / %q", c, funds, basis)
		}
	}
}

func TestHorizonDistinguishesFundsFromTransientFailures(t *testing.T) {
	fundsRelated, _ := predict.Horizon(domain.ClassRetryLater)
	transient, _ := predict.Horizon(domain.ClassRetryNow)
	if fundsRelated == transient {
		t.Fatal("a funds-related failure and a transient failure must not get the same prediction")
	}
}
