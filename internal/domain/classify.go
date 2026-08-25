package domain

import "context"

type Classifier interface {
	Propose(ctx context.Context, code FailureCode, data ClassificationContext) (Proposal, error)
}

type ClassificationContext struct {
	Rail          Rail
	AmountPaise   int64
	AttemptsUsed  int16
	PriorCodes    []FailureCode
	PriorOutcomes []Outcome
}

type Proposal struct {
	Class      FailureClass
	Confidence float64
	Rationale  string
}

type DecisionReason struct {
	FailureCode     FailureCode       `json:"failureCode"`
	Class           FailureClass      `json:"class"`
	ClassifiedBy    ClassifiedBy      `json:"classifiedBy"`
	Confidence      *float64          `json:"confidence"`
	PredictedFunds  string            `json:"predictedFunds"`
	PredictionBasis string            `json:"predictionBasis"`
	Constraints     ReasonConstraints `json:"constraints"`
	BudgetBefore    int16             `json:"budgetBefore"`
	BudgetAfter     int16             `json:"budgetAfter"`
}

type ReasonConstraints struct {
	BlockedWindowShift string `json:"blockedWindowShift"`
	NoticeDeadline     string `json:"noticeDeadline"`
	RailRules          string `json:"railRules"`
}
