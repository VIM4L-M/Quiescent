package domain

import (
	"time"

	_ "time/tzdata"
)

var IST = mustLoadIST()

func mustLoadIST() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		panic(err)
	}
	return loc
}

const (
	blockedFromMinute = 10 * 60
	blockedToMinute   = 13 * 60
)

func Blocked(t time.Time) bool {
	local := t.In(IST)
	m := local.Hour()*60 + local.Minute()
	return m >= blockedFromMinute && m <= blockedToMinute
}
