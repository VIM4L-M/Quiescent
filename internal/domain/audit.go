package domain

import (
	"encoding/json"
	"time"
)

type AuditEntry struct {
	ID            int64           `db:"id"`
	CycleID       CycleID         `db:"cycle_id"`
	CorrelationID CorrelationID   `db:"correlation_id"`
	At            time.Time       `db:"at"`
	Event         string          `db:"event"`
	Inputs        json.RawMessage `db:"inputs"`
	Decision      json.RawMessage `db:"decision"`
	Reason        string          `db:"reason"`
}
