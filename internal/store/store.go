package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound            = errors.New("store: not found")
	ErrConflict            = errors.New("store: guarded update matched no rows")
	ErrDuplicate           = errors.New("store: duplicate key")
	ErrInvalidEnum         = errors.New("store: invalid enum value")
	ErrInvalidArgument     = errors.New("store: invalid argument")
	ErrWriteAheadViolation = errors.New("store: attempt must be inserted unresolved")
)

type querier interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	pool *pgxpool.Pool
	q    querier
}

func Open(ctx context.Context, dbURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool, q: pool}, nil
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Tx(ctx context.Context, fn func(*Store) error) error {
	tx, err := s.q.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(&Store{pool: s.pool, q: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

func mapError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%s: %w: %s", op, ErrDuplicate, pgErr.ConstraintName)
		case "23514":
			return fmt.Errorf("%s: %w: %s", op, ErrConflict, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

func expectOne(op string, tag pgconn.CommandTag, err error) error {
	if err != nil {
		return mapError(op, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%s: %w", op, ErrConflict)
	}
	return nil
}
