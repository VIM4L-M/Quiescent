package domain

type State string

const (
	StatePending   State = "pending"
	StateScheduled State = "scheduled"
	StateInFlight  State = "in_flight"
	StateUnknown   State = "unknown"
	StateHeld      State = "held"
	StateRecovered State = "recovered"
	StateEscalated State = "escalated"
	StateAbandoned State = "abandoned"
)

func (s State) Valid() bool {
	switch s {
	case StatePending, StateScheduled, StateInFlight, StateUnknown,
		StateHeld, StateRecovered, StateEscalated, StateAbandoned:
		return true
	}
	return false
}

func (s State) Terminal() bool {
	switch s {
	case StateRecovered, StateEscalated, StateAbandoned:
		return true
	}
	return false
}

type Disposition string

const (
	DispositionRecovered Disposition = "recovered"
	DispositionEscalated Disposition = "escalated"
	DispositionAbandoned Disposition = "abandoned"
)

func (d Disposition) Valid() bool {
	switch d {
	case DispositionRecovered, DispositionEscalated, DispositionAbandoned:
		return true
	}
	return false
}

type Rail string

const (
	RailUPIAutopay Rail = "upi_autopay"
	RailENACH      Rail = "enach"
)

func (r Rail) Valid() bool {
	switch r {
	case RailUPIAutopay, RailENACH:
		return true
	}
	return false
}

type Outcome string

const (
	OutcomeSuccess        Outcome = "SUCCESS"
	OutcomeFailure        Outcome = "FAILURE"
	OutcomeTimeout        Outcome = "TIMEOUT"
	OutcomeAbandonedStale Outcome = "ABANDONED_STALE"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomeSuccess, OutcomeFailure, OutcomeTimeout, OutcomeAbandonedStale:
		return true
	}
	return false
}

type FailureCode string

const (
	FailureInsufficientFunds     FailureCode = "INSUFFICIENT_FUNDS"
	FailureMandateRevoked        FailureCode = "MANDATE_REVOKED"
	FailureAmountExceedsLimit    FailureCode = "AMOUNT_EXCEEDS_LIMIT"
	FailurePreDebitNoticeMissing FailureCode = "PRE_DEBIT_NOTICE_MISSING"
)

type FailureClass string

const (
	ClassRetryNow     FailureClass = "retry_now"
	ClassRetryLater   FailureClass = "retry_later"
	ClassTerminal     FailureClass = "terminal"
	ClassManualReview FailureClass = "manual_review"
)

func (c FailureClass) Valid() bool {
	switch c {
	case ClassRetryNow, ClassRetryLater, ClassTerminal, ClassManualReview:
		return true
	}
	return false
}

type ClassifiedBy string

const (
	ClassifiedByTable ClassifiedBy = "table"
	ClassifiedByAI    ClassifiedBy = "ai"
)

func (c ClassifiedBy) Valid() bool {
	switch c {
	case ClassifiedByTable, ClassifiedByAI:
		return true
	}
	return false
}

type OutboxKind string

const (
	OutboxPreDebitNotice OutboxKind = "pre_debit_notice"
	OutboxEscalation     OutboxKind = "escalation"
)

func (k OutboxKind) Valid() bool {
	switch k {
	case OutboxPreDebitNotice, OutboxEscalation:
		return true
	}
	return false
}
