package provider

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

var (
	ErrSeedMismatch = errors.New("provider: ledger seed differs from configured seed")
	ErrLedgerPath   = errors.New("provider: ledger path is required")
)

type entryKind string

const (
	kindHeader entryKind = "header"
	kindDebit  entryKind = "debit"
	kindFence  entryKind = "fence"
	kindRevoke entryKind = "revoke"
)

type Entry struct {
	Kind           entryKind          `json:"kind"`
	Seed           int64              `json:"seed,omitempty"`
	IdempotencyKey string             `json:"idempotencyKey,omitempty"`
	RequestHash    string             `json:"requestHash,omitempty"`
	CycleID        domain.CycleID     `json:"cycleID,omitempty"`
	AttemptNumber  int                `json:"attemptNumber,omitempty"`
	Fence          int64              `json:"fence,omitempty"`
	Outcome        domain.Outcome     `json:"outcome,omitempty"`
	FailureCode    domain.FailureCode `json:"failureCode,omitempty"`
	AmountPaise    int64              `json:"amountPaise,omitempty"`
	BouncePaise    int64              `json:"bouncePaise,omitempty"`
	At             time.Time          `json:"at"`
}

type Ledger struct {
	mu      sync.Mutex
	file    *os.File
	seed    int64
	debits  map[string]Entry
	fences  map[domain.CycleID]int64
	revoked map[domain.CycleID]bool
}

func OpenLedger(path string, seed int64) (*Ledger, error) {
	if path == "" {
		return nil, ErrLedgerPath
	}
	l := &Ledger{
		seed:    seed,
		debits:  map[string]Entry{},
		fences:  map[domain.CycleID]int64{},
		revoked: map[domain.CycleID]bool{},
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	l.file = f
	if err := l.replay(); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	if len(l.debits) == 0 && len(l.fences) == 0 && len(l.revoked) == 0 {
		if err := l.write(Entry{Kind: kindHeader, Seed: seed, At: time.Now().UTC()}); err != nil {
			f.Close()
			return nil, err
		}
	}
	return l, nil
}

func (l *Ledger) replay() error {
	if _, err := l.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	sc := bufio.NewScanner(l.file)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return fmt.Errorf("provider: ledger line %d: %w", line, err)
		}
		switch e.Kind {
		case kindHeader:
			if e.Seed != l.seed {
				return fmt.Errorf("%w: ledger %d, configured %d", ErrSeedMismatch, e.Seed, l.seed)
			}
		case kindDebit:
			l.debits[e.IdempotencyKey] = e
		case kindFence:
			if e.Fence > l.fences[e.CycleID] {
				l.fences[e.CycleID] = e.Fence
			}
		case kindRevoke:
			l.revoked[e.CycleID] = true
		}
	}
	return sc.Err()
}

func (l *Ledger) write(e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if _, err := l.file.Write(raw); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *Ledger) Lookup(key string) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.debits[key]
	return e, ok
}

func (l *Ledger) HighestFence(cycleID domain.CycleID) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fences[cycleID]
}

func (l *Ledger) AdmitFence(cycleID domain.CycleID, fence int64) (int64, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	highest := l.fences[cycleID]
	if fence < highest {
		return highest, false, nil
	}
	if fence > highest {
		e := Entry{Kind: kindFence, CycleID: cycleID, Fence: fence, At: time.Now().UTC()}
		if err := l.write(e); err != nil {
			return highest, false, err
		}
		l.fences[cycleID] = fence
	}
	return fence, true, nil
}

func (l *Ledger) Revoked(cycleID domain.CycleID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.revoked[cycleID]
}

func (l *Ledger) Revoke(cycleID domain.CycleID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.revoked[cycleID] {
		return nil
	}
	e := Entry{Kind: kindRevoke, CycleID: cycleID, At: time.Now().UTC()}
	if err := l.write(e); err != nil {
		return err
	}
	l.revoked[cycleID] = true
	return nil
}

func (l *Ledger) Commit(e Entry) (Entry, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if prior, ok := l.debits[e.IdempotencyKey]; ok {
		return prior, true, nil
	}
	e.Kind = kindDebit
	if err := l.write(e); err != nil {
		return e, false, err
	}
	l.debits[e.IdempotencyKey] = e
	return e, false, nil
}

func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

func RequestHash(req DebitRequest, attemptNumber int) string {
	h := sha256.New()
	h.Write([]byte(req.CycleID))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(attemptNumber)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(req.AmountPaise, 10)))
	h.Write([]byte{0})
	h.Write([]byte(req.Rail))
	return hex.EncodeToString(h.Sum(nil))
}
