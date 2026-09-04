package store

import (
	"context"

	"github.com/VIM4L-M/Quiescent/internal/domain"
	"github.com/jackc/pgx/v5"
)

const auditColumns = `id, cycle_id, correlation_id, at, event, inputs, decision, reason`

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

func (s *Store) AuditByCycle(ctx context.Context, cycleID domain.CycleID) ([]domain.AuditEntry, error) {
	const q = `SELECT ` + auditColumns + ` FROM audit_log WHERE cycle_id = $1 ORDER BY id`
	rows, err := s.q.Query(ctx, q, cycleID)
	if err != nil {
		return nil, mapError("store: audit by cycle", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByName[domain.AuditEntry])
	if err != nil {
		return nil, mapError("store: audit by cycle", err)
	}
	return entries, nil
}
