package provider

import (
	"sync"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

type InjectMode string

const (
	InjectClear               InjectMode = "clear"
	InjectTimeoutAfterCommit  InjectMode = "timeoutAfterCommit"
	InjectTimeoutBeforeCommit InjectMode = "timeoutBeforeCommit"
	InjectDowntime            InjectMode = "downtime"
	InjectLatency             InjectMode = "latency"
	InjectRevokeMandate       InjectMode = "revokeMandate"
)

func (m InjectMode) Valid() bool {
	switch m {
	case InjectClear, InjectTimeoutAfterCommit, InjectTimeoutBeforeCommit,
		InjectDowntime, InjectLatency, InjectRevokeMandate:
		return true
	}
	return false
}

const unlimited = -1

type fault struct {
	remaining int
	duration  time.Duration
}

type Faults struct {
	mu       sync.Mutex
	global   map[InjectMode]*fault
	perCycle map[domain.CycleID]map[InjectMode]*fault
}

func NewFaults() *Faults {
	return &Faults{
		global:   map[InjectMode]*fault{},
		perCycle: map[domain.CycleID]map[InjectMode]*fault{},
	}
}

func (f *Faults) Set(req InjectRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	remaining := req.Count
	if remaining <= 0 {
		remaining = unlimited
	}
	fl := &fault{remaining: remaining, duration: time.Duration(req.DurationMS) * time.Millisecond}
	if req.CycleID == "" {
		f.global[req.Mode] = fl
		return
	}
	if f.perCycle[req.CycleID] == nil {
		f.perCycle[req.CycleID] = map[InjectMode]*fault{}
	}
	f.perCycle[req.CycleID][req.Mode] = fl
}

func (f *Faults) Clear(cycleID domain.CycleID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cycleID == "" {
		f.global = map[InjectMode]*fault{}
		f.perCycle = map[domain.CycleID]map[InjectMode]*fault{}
		return
	}
	delete(f.perCycle, cycleID)
}

func (f *Faults) Take(cycleID domain.CycleID, mode InjectMode) (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if scoped, ok := f.perCycle[cycleID]; ok {
		if d, hit := take(scoped, mode); hit {
			return d, true
		}
	}
	return take(f.global, mode)
}

func take(m map[InjectMode]*fault, mode InjectMode) (time.Duration, bool) {
	fl, ok := m[mode]
	if !ok {
		return 0, false
	}
	if fl.remaining == unlimited {
		return fl.duration, true
	}
	fl.remaining--
	if fl.remaining <= 0 {
		delete(m, mode)
	}
	return fl.duration, true
}
