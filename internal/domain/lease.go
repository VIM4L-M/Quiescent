package domain

import "time"

type Lease struct {
	CycleID   CycleID   `db:"cycle_id"`
	Holder    *string   `db:"holder"`
	Fence     int64     `db:"fence"`
	ExpiresAt time.Time `db:"expires_at"`
}
