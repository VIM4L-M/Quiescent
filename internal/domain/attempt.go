package domain

import "time"

type Attempt struct {
	AttemptID      AttemptID      `db:"attempt_id"`
	CycleID        CycleID        `db:"cycle_id"`
	Seq            int16          `db:"seq"`
	IdempotencyKey string         `db:"idempotency_key"`
	Fence          *int64         `db:"fence"`
	ScheduledFor   time.Time      `db:"scheduled_for"`
	FiredAt        *time.Time     `db:"fired_at"`
	Outcome        *Outcome       `db:"outcome"`
	FailureCode    *FailureCode   `db:"failure_code"`
	DecisionReason DecisionReason `db:"decision_reason"`
}

func (a Attempt) Fired() bool {
	return a.FiredAt != nil
}

func (a Attempt) Resolved() bool {
	return a.Outcome != nil
}
