package provider

import (
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"os"
	"sort"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

const (
	FailurePSPUnavailable   domain.FailureCode = "PSP_UNAVAILABLE"
	FailurePayerAuthPending domain.FailureCode = "PAYER_AUTH_PENDING"
	FailureTechnicalDecline domain.FailureCode = "TECHNICAL_DECLINE"
	FailureBankUnreachable  domain.FailureCode = "BANK_UNREACHABLE"
	FailureAccountFrozen    domain.FailureCode = "ACCOUNT_FROZEN"
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

type RailRules struct {
	CapPaise      int64
	BouncePaise   int64
	BaseSuccess   float64
	Insufficient  domain.FailureCode
	Revoked       domain.FailureCode
	OverLimit     domain.FailureCode
	WindowDecline domain.FailureCode
	Transient     []domain.FailureCode
}

var railRules = map[domain.Rail]RailRules{
	domain.RailUPIAutopay: {
		CapPaise:      1_500_000,
		BouncePaise:   0,
		BaseSuccess:   0.88,
		Insufficient:  domain.FailureInsufficientFunds,
		Revoked:       domain.FailureMandateRevoked,
		OverLimit:     domain.FailureAmountExceedsLimit,
		WindowDecline: FailureTechnicalDecline,
		Transient:     []domain.FailureCode{FailurePSPUnavailable, FailurePayerAuthPending},
	},
	domain.RailENACH: {
		CapPaise:      10_000_000,
		BouncePaise:   29_500,
		BaseSuccess:   0.78,
		Insufficient:  domain.FailureInsufficientFunds,
		Revoked:       domain.FailureMandateRevoked,
		OverLimit:     domain.FailureAmountExceedsLimit,
		WindowDecline: FailureTechnicalDecline,
		Transient:     []domain.FailureCode{FailureBankUnreachable, FailureAccountFrozen},
	},
}

func RulesFor(rail domain.Rail) (RailRules, bool) {
	r, ok := railRules[rail]
	return r, ok
}

type BalancePoint struct {
	From         time.Time `json:"from"`
	BalancePaise int64     `json:"balancePaise"`
}

type Customer struct {
	CustomerID domain.CustomerID `json:"customerID"`
	Timeline   []BalancePoint    `json:"timeline,omitempty"`
	Payday     int               `json:"payday,omitempty"`
	LowPaise   int64             `json:"lowPaise,omitempty"`
	HighPaise  int64             `json:"highPaise,omitempty"`
}

func (c Customer) BalanceAt(t time.Time) int64 {
	if len(c.Timeline) > 0 {
		b := c.Timeline[0].BalancePaise
		for _, p := range c.Timeline {
			if p.From.After(t) {
				break
			}
			b = p.BalancePaise
		}
		return b
	}
	if c.Payday == 0 {
		return 0
	}
	if t.In(IST).Day() >= c.Payday {
		return c.HighPaise
	}
	return c.LowPaise
}

type CycleFixture struct {
	CycleID     domain.CycleID    `json:"cycleID"`
	CustomerID  domain.CustomerID `json:"customerID"`
	Rail        domain.Rail       `json:"rail"`
	AmountPaise int64             `json:"amountPaise"`
	DueDate     time.Time         `json:"dueDate"`
}

type Fixture struct {
	Customers []Customer     `json:"customers"`
	Cycles    []CycleFixture `json:"cycles"`
}

func LoadFixture(path string) (Fixture, error) {
	var f Fixture
	raw, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, err
	}
	return f, nil
}

type World struct {
	mu        sync.RWMutex
	seed      int64
	customers map[domain.CustomerID]Customer
	cycles    map[domain.CycleID]CycleFixture
}

func NewWorld(seed int64) *World {
	return &World{
		seed:      seed,
		customers: map[domain.CustomerID]Customer{},
		cycles:    map[domain.CycleID]CycleFixture{},
	}
}

func (w *World) Apply(f Fixture) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range f.Customers {
		sort.Slice(c.Timeline, func(i, j int) bool { return c.Timeline[i].From.Before(c.Timeline[j].From) })
		w.customers[c.CustomerID] = c
	}
	for _, c := range f.Cycles {
		if c.CustomerID == "" {
			c.CustomerID = domain.CustomerID(c.CycleID)
		}
		w.cycles[c.CycleID] = c
	}
}

func (w *World) Cycle(cycleID domain.CycleID) CycleFixture {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if c, ok := w.cycles[cycleID]; ok {
		return c
	}
	return CycleFixture{CycleID: cycleID, CustomerID: domain.CustomerID(cycleID)}
}

func (w *World) Customer(id domain.CustomerID) Customer {
	w.mu.RLock()
	c, ok := w.customers[id]
	w.mu.RUnlock()
	if ok {
		return c
	}
	return synthCustomer(w.seed, id)
}

func (w *World) BalanceAt(cycleID domain.CycleID, t time.Time) int64 {
	return w.Customer(w.Cycle(cycleID).CustomerID).BalanceAt(t)
}

func synthCustomer(seed int64, id domain.CustomerID) Customer {
	h := hashString(seed, "customer", string(id))
	return Customer{
		CustomerID: id,
		Payday:     1 + int(h%28),
		LowPaise:   10_000 + int64((h>>8)%90_000),
		HighPaise:  500_000 + int64((h>>24)%2_500_000),
	}
}

func hashString(seed int64, salt, s string) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(seed))
	h.Write(buf[:])
	h.Write([]byte{0})
	h.Write([]byte(salt))
	h.Write([]byte{0})
	h.Write([]byte(s))
	return h.Sum64()
}
