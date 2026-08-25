package domain

import (
	"encoding/json"
	"time"
)

type OutboxEntry struct {
	ID          int64           `db:"id"`
	CycleID     CycleID         `db:"cycle_id"`
	AttemptID   AttemptID       `db:"attempt_id"`
	Kind        OutboxKind      `db:"kind"`
	Payload     json.RawMessage `db:"payload"`
	DeliverBy   time.Time       `db:"deliver_by"`
	DeliveredAt *time.Time      `db:"delivered_at"`
	Attempts    int16           `db:"attempts"`
}

func (o OutboxEntry) Delivered() bool {
	return o.DeliveredAt != nil
}
