// Package postgres provides a PostgreSQL-backed agent.ExecutionStore.
//
// Each execution is one row. Create inserts it, Save updates it with a
// compare-and-swap on the version column, and Load reads it. The row holds only
// resumable runtime state and the conversation cursor; the transcript stays in
// the ConversationStore.
//
// # Table schema
//
// The caller creates the table. Expected schema:
//
//	CREATE TABLE executions (
//	    execution_id TEXT        PRIMARY KEY,
//	    version      BIGINT      NOT NULL CHECK (version > 0),
//	    payload      JSON        NOT NULL,
//	    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
//	    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);
//
// The payload is JSON rather than JSONB on purpose: JSON keeps the stored text
// byte for byte, so raw tool inputs and pause payloads round trip exactly,
// while JSONB would normalize whitespace and key order. It is still queryable
// with the ->> operators.
//
// The package does not read or write any table used by earlier releases; old
// rows may remain untouched.
//
// # Usage
//
//	executions := postgres.New(pool)
//	a, err := agent.New(provider, instructions,
//	    agent.WithConversationStore(conversations),
//	    agent.WithExecutionStore(executions),
//	)
//
// Import it with an alias when you also use a PostgreSQL conversation store:
//
//	pgexec "github.com/camilbinas/gude-agents/agent/execution/postgres"
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/record"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the subset of *pgxpool.Pool (or pgx.Conn) the store needs.
type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ agent.ExecutionStore = (*Store)(nil)

// Store is an agent.ExecutionStore backed by PostgreSQL.
type Store struct {
	db    DB
	table string
	err   error
}

// New returns a Store over a connected pool. A nil database or an invalid table
// name does not panic: every operation returns the configuration error. The
// table must already exist with the documented schema.
func New(db DB, opts ...Option) *Store {
	cfg := config{table: defaultTable}
	for _, opt := range opts {
		opt(&cfg)
	}
	s := &Store{db: db}
	switch {
	case db == nil:
		s.err = errors.New("execution/postgres: database is required")
	case cfg.table == "" || strings.ContainsRune(cfg.table, 0):
		s.err = fmt.Errorf("execution/postgres: table name %q is invalid", cfg.table)
	default:
		s.table = pgx.Identifier{cfg.table}.Sanitize()
	}
	return s
}

// Create inserts a new execution at version 1. The ID must be absent.
func (s *Store) Create(ctx context.Context, execution agent.Execution) (agent.Execution, error) {
	if err := s.check(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	tag, err := s.db.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (execution_id, version, payload) VALUES ($1, 1, $2) ON CONFLICT (execution_id) DO NOTHING`, s.table),
		execution.ID, payload)
	if err != nil {
		return agent.Execution{}, record.Wrap("create", err)
	}
	if tag.RowsAffected() == 0 {
		return agent.Execution{}, record.Conflict("create", execution.ID)
	}
	return record.Unmarshal(payload, execution.ID, 1)
}

// Load returns the current execution.
func (s *Store) Load(ctx context.Context, id string) (agent.Execution, error) {
	if err := s.check(id); err != nil {
		return agent.Execution{}, err
	}
	var (
		version int64
		payload []byte
	)
	err := s.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT version, payload FROM %s WHERE execution_id = $1`, s.table), id).Scan(&version, &payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agent.Execution{}, record.NotFound(id)
		}
		return agent.Execution{}, record.Wrap("load", err)
	}
	v, err := record.Version(version)
	if err != nil {
		return agent.Execution{}, err
	}
	return record.Unmarshal(payload, id, v)
}

// Save replaces the execution when expectedVersion is exactly the stored
// version. The comparison and the write are one atomic UPDATE, so concurrent
// callers cannot both succeed; a stale or missing version is a conflict.
func (s *Store) Save(ctx context.Context, execution agent.Execution, expectedVersion uint64) (agent.Execution, error) {
	if err := s.check(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	if err := record.CheckExpected(execution.ID, expectedVersion); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	var version int64
	err = s.db.QueryRow(ctx,
		fmt.Sprintf(`UPDATE %s SET version = version + 1, payload = $3, updated_at = NOW() WHERE execution_id = $1 AND version = $2 RETURNING version`, s.table),
		execution.ID, int64(expectedVersion), payload).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agent.Execution{}, record.Conflict("save", execution.ID)
		}
		return agent.Execution{}, record.Wrap("save", err)
	}
	v, err := record.Version(version)
	if err != nil {
		return agent.Execution{}, err
	}
	return record.Unmarshal(payload, execution.ID, v)
}

func (s *Store) check(id string) error {
	if s == nil {
		return errors.New("execution/postgres: store is nil")
	}
	if s.err != nil {
		return s.err
	}
	return record.ValidateID(id)
}
