package domain

import "time"

type BalanceTrigger struct {
	TriggerID   TriggerID  `db:"trigger_id"`
	CycleID     CycleID    `db:"cycle_id"`
	Seq         int16      `db:"seq"`
	SentAt      time.Time  `db:"sent_at"`
	ExpiresAt   time.Time  `db:"expires_at"`
	RespondedAt *time.Time `db:"responded_at"`
	Response    *string    `db:"response"`
}

func (t BalanceTrigger) Answered() bool {
	return t.RespondedAt != nil
}

func (t BalanceTrigger) SaidYes() bool {
	return t.Response != nil && *t.Response == "yes"
}

func (t BalanceTrigger) Expired(now time.Time) bool {
	return now.After(t.ExpiresAt)
}
