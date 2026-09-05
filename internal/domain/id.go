package domain

import (
	"crypto/rand"
	"fmt"
	"strconv"
)

type CycleID string

type MandateID string

type CustomerID string

type AttemptID string

type TriggerID string

type CorrelationID string

type Fence int64

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func NewAttemptID() AttemptID {
	return AttemptID(newUUID())
}

func NewCycleID() CycleID {
	return CycleID(newUUID())
}

func NewMandateID() MandateID {
	return MandateID(newUUID())
}

func NewCustomerID() CustomerID {
	return CustomerID(newUUID())
}

func NewCorrelationID() CorrelationID {
	return CorrelationID(newUUID())
}

func NewTriggerID() TriggerID {
	return TriggerID(newUUID())
}

func NewIdempotencyKey(cycleID CycleID, seq int16) string {
	return string(cycleID) + ":" + strconv.Itoa(int(seq))
}
