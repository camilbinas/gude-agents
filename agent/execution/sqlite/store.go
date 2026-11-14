// Package sqlite provides a SQLite-backed agent.ExecutionStore.
//
// It needs no server, so a paused execution survives a process restart with a
// single local file. Each execution is one row: Create inserts it at version 1,
// Save replaces it with a compare-and-swap on the version, and Load reads it.
// The transcript stays in the ConversationStore.
//
// New creates the table if it does not exist:
//
//	CREATE TABLE executions (
//	    execution_id TEXT    PRIMARY KEY,
//	    version      INTEGER NOT NULL CHECK (version > 0),
//	    payload      TEXT    NOT NULL,
//	    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
//	    updated_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
//	);
//
// The payload is the execution as JSON text, stored byte for byte, so it can be
// queried with SQLite's JSON functions.
//
// # Usage
//
//	executions, err := sqlite.New("file:agent.db")
//	if err != nil { ... }
//	defer executions.Close()
//	a, err := agent.Agent(provider, instructions,
//	    agent.WithConversationStore(conversations),
//	    agent.WithExecutionStore(executions),
//	)
//
// The conversation and execution stores can share one database file. Import
// this package with an alias when you also use the SQLite conversation store:
//
//	sqliteexec "github.com/camilbinas/gude-agents/agent/execution/sqlite"
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/record"

	_ "modernc.org/sqlite"
)

var _ agent.ExecutionStore = (*Store)(nil)

// Store is an agent.ExecutionStore backed by SQLite.
type Store struct {
	db    *sql.DB
	table string
}

// New opens dsn, creates the executions table if needed, and returns a Store.
// Close it when done. An in-memory DSN (":memory:" or mode=memory) keeps one
// connection, because every SQLite connection would otherwise get its own
// empty database.
func New(dsn string, opts ...Option) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("execution/sqlite: dsn is required")
	}
	cfg := config{table: defaultTable, busyTimeout: 5 * time.Second}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.table == "" || strings.ContainsRune(cfg.table, 0) {
		return nil, fmt.Errorf("execution/sqlite: table name %q is invalid", cfg.table)
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	memory := strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")
	full := fmt.Sprintf("%s%s_pragma=busy_timeout(%d)&_txlock=immediate", dsn, separator, cfg.busyTimeout.Milliseconds())
	if !memory {
		full += "&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", full)
	if err != nil {
		return nil, fmt.Errorf("execution/sqlite: open: %w", err)
	}
	if memory {
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db, table: identifier(cfg.table)}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		execution_id TEXT    PRIMARY KEY,
		version      INTEGER NOT NULL CHECK (version > 0),
		payload      TEXT    NOT NULL,
		created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`, s.table)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("execution/sqlite: create table: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
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
	res, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (execution_id, version, payload) VALUES (?, 1, ?) ON CONFLICT (execution_id) DO NOTHING`, s.table),
		execution.ID, string(payload))
	if err != nil {
		return agent.Execution{}, record.Wrap("create", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return agent.Execution{}, record.Wrap("create", err)
	} else if n == 0 {
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
		payload string
	)
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT version, payload FROM %s WHERE execution_id = ?`, s.table), id).Scan(&version, &payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agent.Execution{}, record.NotFound(id)
		}
		return agent.Execution{}, record.Wrap("load", err)
	}
	v, err := record.Version(version)
	if err != nil {
		return agent.Execution{}, err
	}
	return record.Unmarshal([]byte(payload), id, v)
}

// Save replaces the execution when expectedVersion is exactly the stored
// version. The check and the write are one UPDATE, so concurrent callers
// cannot both succeed; a stale or missing version is a conflict.
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
	err = s.db.QueryRowContext(ctx,
		fmt.Sprintf(`UPDATE %s SET version = version + 1, payload = ?, updated_at = CURRENT_TIMESTAMP WHERE execution_id = ? AND version = ? RETURNING version`, s.table),
		string(payload), execution.ID, int64(expectedVersion)).Scan(&version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	if s == nil || s.db == nil {
		return errors.New("execution/sqlite: store is nil")
	}
	return record.ValidateID(id)
}

func identifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
