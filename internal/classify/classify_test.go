package classify_test

import (
	"testing"

	"github.com/VIM4L-M/Quiescent/internal/classify"
	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func TestClassifyKnownCodes(t *testing.T) {
	cases := []struct {
		code domain.FailureCode
		want domain.FailureClass
	}{
		{domain.FailureInsufficientFunds, domain.ClassRetryLater},
		{domain.FailureMandateRevoked, domain.ClassTerminal},
		{domain.FailureAmountExceedsLimit, domain.ClassTerminal},
		{domain.FailurePreDebitNoticeMissing, domain.ClassRetryLater},
		{"PSP_UNAVAILABLE", domain.ClassRetryNow},
		{"PAYER_AUTH_PENDING", domain.ClassRetryNow},
		{"TECHNICAL_DECLINE", domain.ClassRetryNow},
		{"BANK_UNREACHABLE", domain.ClassRetryNow},
		{"ACCOUNT_FROZEN", domain.ClassManualReview},
	}
	for _, c := range cases {
		got, ok := classify.Classify(c.code)
		if !ok {
			t.Errorf("%s: expected a known mapping", c.code)
		}
		if got != c.want {
			t.Errorf("%s: got %s want %s", c.code, got, c.want)
		}
	}
}

func TestClassifyUnknownCodeDefaultsToManualReview(t *testing.T) {
	got, ok := classify.Classify("SOME_NEW_CODE_NOBODY_HAS_SEEN")
	if ok {
		t.Fatal("an unmapped code must report ok=false")
	}
	if got != domain.ClassManualReview {
		t.Fatalf("unmapped code: got %s want %s — uncertainty must fail safe", got, domain.ClassManualReview)
	}
}

func TestClassifyAlwaysReturnsAValidClass(t *testing.T) {
	codes := []domain.FailureCode{
		domain.FailureInsufficientFunds, domain.FailureMandateRevoked,
		domain.FailureAmountExceedsLimit, domain.FailurePreDebitNoticeMissing,
		"PSP_UNAVAILABLE", "PAYER_AUTH_PENDING", "TECHNICAL_DECLINE",
		"BANK_UNREACHABLE", "ACCOUNT_FROZEN", "UNKNOWN_CODE_1", "UNKNOWN_CODE_2", "",
	}
	for _, code := range codes {
		class, _ := classify.Classify(code)
		if !class.Valid() {
			t.Errorf("%q produced an invalid class %q", code, class)
		}
	}
}
