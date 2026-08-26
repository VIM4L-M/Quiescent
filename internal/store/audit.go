package store

import (
	"context"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func (s *Store) AppendAudit(ctx context.Context, e domain.AuditEntry) error {
	if err := validateAudit(e); err != nil {
		return err
	}
	const q = `
		INSERT INTO audit_log (cycle_id, correlation_id, event, inputs, decision, reason)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := s.q.Exec(ctx, q,
		e.CycleID, e.CorrelationID, e.Event, e.Inputs, e.Decision, e.Reason)
	return mapError("store: append audit", err)
}
