package provider

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

type Conditions struct {
	Seed           int64
	CycleID        domain.CycleID
	AttemptNumber  int
	Rail           domain.Rail
	AmountPaise    int64
	BalancePaise   int64
	FiredAt        time.Time
	MandateRevoked bool
}

type Decision struct {
	Outcome     domain.Outcome
	FailureCode domain.FailureCode
	BouncePaise int64
}

func Hash64(seed int64, cycleID domain.CycleID, attemptNumber int) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(seed))
	h.Write(buf[:])
	h.Write([]byte{0})
	h.Write([]byte(cycleID))
	h.Write([]byte{0})
	binary.BigEndian.PutUint64(buf[:], uint64(attemptNumber))
	h.Write(buf[:])
	return h.Sum64()
}

func Draw(seed int64, cycleID domain.CycleID, attemptNumber int) float64 {
	return float64(Hash64(seed, cycleID, attemptNumber)>>11) / float64(uint64(1)<<53)
}

func SuccessProbability(c Conditions) float64 {
	rules, ok := RulesFor(c.Rail)
	if !ok {
		return 0
	}
	if c.MandateRevoked || Blocked(c.FiredAt) {
		return 0
	}
	if c.AmountPaise <= 0 || c.AmountPaise > rules.CapPaise {
		return 0
	}
	if c.BalancePaise < c.AmountPaise {
		return 0
	}
	p := rules.BaseSuccess
	headroom := float64(c.BalancePaise) / float64(c.AmountPaise)
	switch {
	case headroom >= 2:
		p += 0.05
	case headroom < 1.2:
		p -= 0.15
	}
	if c.AttemptNumber > 1 {
		p *= math.Pow(0.95, float64(c.AttemptNumber-1))
	}
	return math.Min(0.98, math.Max(0.05, p))
}

func Decide(c Conditions) Decision {
	rules, ok := RulesFor(c.Rail)
	if !ok {
		return Decision{Outcome: domain.OutcomeFailure, FailureCode: FailureTechnicalDecline}
	}
	if c.MandateRevoked {
		return Decision{Outcome: domain.OutcomeFailure, FailureCode: rules.Revoked}
	}
	if Blocked(c.FiredAt) {
		return Decision{Outcome: domain.OutcomeFailure, FailureCode: rules.WindowDecline}
	}
	if c.AmountPaise <= 0 || c.AmountPaise > rules.CapPaise {
		return Decision{Outcome: domain.OutcomeFailure, FailureCode: rules.OverLimit}
	}
	if c.BalancePaise < c.AmountPaise {
		return Decision{Outcome: domain.OutcomeFailure, FailureCode: rules.Insufficient, BouncePaise: rules.BouncePaise}
	}
	v := Hash64(c.Seed, c.CycleID, c.AttemptNumber)
	draw := float64(v>>11) / float64(uint64(1)<<53)
	if draw < SuccessProbability(c) {
		return Decision{Outcome: domain.OutcomeSuccess}
	}
	code := rules.Transient[int(v&0x7ff)%len(rules.Transient)]
	return Decision{Outcome: domain.OutcomeFailure, FailureCode: code}
}
