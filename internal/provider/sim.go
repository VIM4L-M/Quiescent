package provider

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type Config struct {
	Seed                 int64
	Addr                 string
	OracleAddr           string
	LedgerPath           string
	FixturePath          string
	IgnoreBlockedWindows bool
}

type Sim struct {
	cfg    Config
	log    *slog.Logger
	ledger *Ledger
	world  *World
	faults *Faults
	Now    func() time.Time
}

func New(cfg Config, log *slog.Logger) (*Sim, error) {
	if log == nil {
		log = slog.Default()
	}
	ledger, err := OpenLedger(cfg.LedgerPath, cfg.Seed)
	if err != nil {
		return nil, err
	}
	world := NewWorld(cfg.Seed)
	if cfg.FixturePath != "" {
		f, err := LoadFixture(cfg.FixturePath)
		if err != nil {
			ledger.Close()
			return nil, err
		}
		world.Apply(f)
	}
	return &Sim{
		cfg:    cfg,
		log:    log,
		ledger: ledger,
		world:  world,
		faults: NewFaults(),
		Now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Sim) Close() error { return s.ledger.Close() }

func (s *Sim) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /debit", s.handleDebit)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("POST /inject", s.handleInject)
	return mux
}

func (s *Sim) OracleHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oracle/probabilities", s.handleOracle)
	return mux
}

func (s *Sim) Run(ctx context.Context) error {
	public := &http.Server{Addr: s.cfg.Addr, Handler: s.Handler()}
	oracle := &http.Server{Addr: s.cfg.OracleAddr, Handler: s.OracleHandler()}

	errs := make(chan error, 2)
	go func() { errs <- listen(public) }()
	go func() { errs <- listen(oracle) }()

	s.log.Info("provider-sim listening",
		"addr", s.cfg.Addr, "oracleAddr", s.cfg.OracleAddr,
		"seed", s.cfg.Seed, "ledger", s.cfg.LedgerPath)

	select {
	case <-ctx.Done():
	case err := <-errs:
		shutdown(public, oracle)
		return err
	}
	shutdown(public, oracle)
	return ctx.Err()
}

func listen(srv *http.Server) error {
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func shutdown(servers ...*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(ctx)
	}
}

func (s *Sim) handleDebit(w http.ResponseWriter, r *http.Request) {
	var req DebitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest, err.Error())
		return
	}
	if req.CycleID == "" || req.IdempotencyKey == "" || !req.Rail.Valid() || req.AmountPaise <= 0 {
		writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest,
			"cycleID, idempotencyKey, rail and a positive amountPaise are required")
		return
	}
	attemptNumber, ok := AttemptNumberFromKey(req.CycleID, req.IdempotencyKey)
	if !ok {
		writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest,
			"idempotencyKey must be cycleID:seq with seq >= 1")
		return
	}

	if _, hit := s.faults.Take(req.CycleID, InjectDowntime); hit {
		s.log.Warn("injected downtime", "cycleID", req.CycleID, "idempotencyKey", req.IdempotencyKey)
		drop(w)
		return
	}
	if d, hit := s.faults.Take(req.CycleID, InjectLatency); hit {
		s.log.Warn("injected latency", "cycleID", req.CycleID, "durationMs", d.Milliseconds())
		select {
		case <-time.After(d):
		case <-r.Context().Done():
		}
	}

	highest, admitted, err := s.ledger.AdmitFence(req.CycleID, req.Fence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeMalformedRequest, err.Error())
		return
	}
	if !admitted {
		s.log.Warn("rejected stale fence",
			"cycleID", req.CycleID, "fence", req.Fence, "highestSeen", highest)
		writeError(w, http.StatusPreconditionFailed, ErrCodeStaleFence,
			"fence is lower than the highest seen for this cycle")
		return
	}

	hash := RequestHash(req, attemptNumber)
	if prior, found := s.ledger.Lookup(req.IdempotencyKey); found {
		if prior.RequestHash != hash {
			s.log.Warn("idempotency conflict",
				"cycleID", req.CycleID, "idempotencyKey", req.IdempotencyKey)
			writeError(w, http.StatusConflict, ErrCodeIdempotencyConflict,
				"idempotency key reused with a different request")
			return
		}
		writeJSON(w, http.StatusOK, responseFrom(prior, true))
		return
	}

	if _, hit := s.faults.Take(req.CycleID, InjectTimeoutBeforeCommit); hit {
		s.log.Warn("injected timeout before commit",
			"cycleID", req.CycleID, "idempotencyKey", req.IdempotencyKey)
		drop(w)
		return
	}

	firedAt := s.Now()
	decision := Decide(Conditions{
		Seed:                s.cfg.Seed,
		CycleID:             req.CycleID,
		AttemptNumber:       attemptNumber,
		Rail:                req.Rail,
		AmountPaise:         req.AmountPaise,
		BalancePaise:        s.world.BalanceAt(req.CycleID, firedAt),
		FiredAt:             firedAt,
		MandateRevoked:      s.ledger.Revoked(req.CycleID),
		IgnoreBlockedWindow: s.cfg.IgnoreBlockedWindows,
	})

	entry := Entry{
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    hash,
		CycleID:        req.CycleID,
		AttemptNumber:  attemptNumber,
		Fence:          req.Fence,
		Outcome:        decision.Outcome,
		FailureCode:    decision.FailureCode,
		AmountPaise:    req.AmountPaise,
		BouncePaise:    decision.BouncePaise,
		At:             firedAt,
	}
	stored, raced, err := s.ledger.Commit(entry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeMalformedRequest, err.Error())
		return
	}
	if raced {
		if stored.RequestHash != hash {
			writeError(w, http.StatusConflict, ErrCodeIdempotencyConflict,
				"idempotency key reused with a different request")
			return
		}
		writeJSON(w, http.StatusOK, responseFrom(stored, true))
		return
	}
	s.log.Info("debit committed",
		"cycleID", req.CycleID, "idempotencyKey", req.IdempotencyKey,
		"attemptNumber", attemptNumber, "fence", req.Fence,
		"outcome", decision.Outcome, "failureCode", decision.FailureCode)

	if _, hit := s.faults.Take(req.CycleID, InjectTimeoutAfterCommit); hit {
		s.log.Warn("injected timeout after commit",
			"cycleID", req.CycleID, "idempotencyKey", req.IdempotencyKey)
		drop(w)
		return
	}
	writeJSON(w, http.StatusOK, responseFrom(entry, false))
}

func (s *Sim) handleStatus(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("idempotencyKey")
	if key == "" {
		writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest, "idempotencyKey is required")
		return
	}
	entry, found := s.ledger.Lookup(key)
	if _, hit := s.faults.Take(entry.CycleID, InjectDowntime); hit {
		s.log.Warn("injected downtime on status", "idempotencyKey", key)
		drop(w)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, ErrCodeUnknownKey,
			"no debit recorded under this idempotency key")
		return
	}
	writeJSON(w, http.StatusOK, responseFrom(entry, true))
}

func (s *Sim) handleInject(w http.ResponseWriter, r *http.Request) {
	var req InjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest, err.Error())
		return
	}
	if !req.Mode.Valid() {
		writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest, "unknown injection mode")
		return
	}
	switch req.Mode {
	case InjectRevokeMandate:
		if req.CycleID == "" {
			writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest,
				"revokeMandate requires a cycleID")
			return
		}
		if err := s.ledger.Revoke(req.CycleID); err != nil {
			writeError(w, http.StatusInternalServerError, ErrCodeMalformedRequest, err.Error())
			return
		}
		s.log.Warn("mandate revoked", "cycleID", req.CycleID)
	case InjectClear:
		s.faults.Clear(req.CycleID)
		s.log.Info("faults cleared", "cycleID", req.CycleID)
	default:
		s.faults.Set(req)
		s.log.Warn("fault armed", "mode", req.Mode, "cycleID", req.CycleID,
			"durationMs", req.DurationMS, "count", req.Count)
	}
	writeJSON(w, http.StatusOK, req)
}

func responseFrom(e Entry, replayed bool) DebitResponse {
	return DebitResponse{
		IdempotencyKey: e.IdempotencyKey,
		CycleID:        e.CycleID,
		AttemptNumber:  e.AttemptNumber,
		Outcome:        e.Outcome,
		FailureCode:    e.FailureCode,
		AmountPaise:    e.AmountPaise,
		BouncePaise:    e.BouncePaise,
		DebitedAt:      e.At,
		Replayed:       replayed,
	}
}

func drop(w http.ResponseWriter) {
	h, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := h.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code ErrorCode, msg string) {
	writeJSON(w, status, ErrorBody{Error: code, Message: msg})
}
