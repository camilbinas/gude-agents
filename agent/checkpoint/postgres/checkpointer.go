// Package postgres provides a PostgreSQL-backed checkpoint.Checkpointer.
//
// # Table Schema
//
// The table must be created by the caller. Expected schema:
//
//	CREATE TABLE checkpoints (
//	    thread_id   TEXT NOT NULL,
//	    version     INTEGER NOT NULL,
//	    label       TEXT NOT NULL DEFAULT '',
//	    state       JSONB NOT NULL,
//	    usage       JSONB NOT NULL,
//	    extra       JSONB,
//	    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
//	    PRIMARY KEY (thread_id, version)
//	);
//
// State and Extra are stored as JSONB. Extra is never decomposed or interpreted,
// so its JSON meaning and consumer-owned fields survive round trips.
//
// # Usage
//
//	cp, err := postgres.New(pool)
//	cp, err := postgres.New(pool, postgres.WithTableName("my_checkpoints"))
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time interface check.
var _ checkpoint.Checkpointer = (*Checkpointer)(nil)

// Checkpointer implements checkpoint.Checkpointer using PostgreSQL.
type Checkpointer struct {
	pool *pgxpool.Pool
	cfg  *pgConfig
}

// New creates a Postgres Checkpointer over a connected pool. The table must
// already exist with the expected schema.
func New(pool *pgxpool.Pool, opts ...Option) (*Checkpointer, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres checkpointer: pool is required")
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	// Validate and sanitize the identifier for safe SQL interpolation.
	if cfg.tableName == "" || strings.ContainsRune(cfg.tableName, 0) {
		return nil, fmt.Errorf("postgres checkpointer: table name %q is invalid", cfg.tableName)
	}
	cfg.tableName = pgx.Identifier{cfg.tableName}.Sanitize()

	return &Checkpointer{pool: pool, cfg: cfg}, nil
}

const selectColumns = `version, label, state, usage, extra, created_at`

// Save appends a checkpoint while serializing version allocation per thread.
func (c *Checkpointer) Save(ctx context.Context, threadID string, cp checkpoint.Checkpoint) (checkpoint.Checkpoint, error) {
	if threadID == "" {
		return checkpoint.Checkpoint{}, checkpoint.ErrThreadIDRequired
	}

	cp.ThreadID = threadID
	if cp.Timestamp.IsZero() {
		cp.Timestamp = time.Now()
	}

	stateJSON, err := json.Marshal(cp.State)
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: save marshal state: %w", err)
	}
	usageJSON, err := json.Marshal(cp.Usage)
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: save marshal usage: %w", err)
	}

	var extraJSON []byte
	if len(cp.Extra) > 0 {
		extraJSON = cp.Extra
	}

	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: save begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, hashtextextended($2, 0)))`, threadID, c.cfg.tableName); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: save lock: %w", err)
	}

	var maxVersion int
	maxQuery := fmt.Sprintf(`SELECT COALESCE(MAX(version), 0) FROM %s WHERE thread_id = $1`, c.cfg.tableName)
	if err := tx.QueryRow(ctx, maxQuery, threadID).Scan(&maxVersion); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: save max version: %w", err)
	}
	cp.Version = maxVersion + 1

	insert := fmt.Sprintf(
		`INSERT INTO %s (thread_id, version, label, state, usage, extra, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.cfg.tableName,
	)
	if _, err := tx.Exec(ctx, insert,
		threadID, cp.Version, cp.Label, stateJSON, usageJSON, extraJSON, cp.Timestamp,
	); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: save insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: save commit: %w", err)
	}

	return cp, nil
}

// Load returns the highest-versioned checkpoint for the thread.
func (c *Checkpointer) Load(ctx context.Context, threadID string) (checkpoint.Checkpoint, error) {
	query := fmt.Sprintf(
		`SELECT %s FROM %s WHERE thread_id = $1 ORDER BY version DESC LIMIT 1`,
		selectColumns, c.cfg.tableName,
	)
	return c.scanOne(ctx, threadID, c.pool.QueryRow(ctx, query, threadID))
}

// LoadAt returns the checkpoint at an exact version.
func (c *Checkpointer) LoadAt(ctx context.Context, threadID string, version int) (checkpoint.Checkpoint, error) {
	query := fmt.Sprintf(
		`SELECT %s FROM %s WHERE thread_id = $1 AND version = $2`,
		selectColumns, c.cfg.tableName,
	)
	return c.scanOne(ctx, threadID, c.pool.QueryRow(ctx, query, threadID, version))
}

// History returns metadata for every checkpoint on the thread, oldest first.
func (c *Checkpointer) History(ctx context.Context, threadID string) ([]checkpoint.Meta, error) {
	query := fmt.Sprintf(
		`SELECT version, label, created_at FROM %s WHERE thread_id = $1 ORDER BY version ASC`,
		c.cfg.tableName,
	)

	rows, err := c.pool.Query(ctx, query, threadID)
	if err != nil {
		return nil, fmt.Errorf("postgres checkpointer: history: %w", err)
	}
	defer rows.Close()

	var metas []checkpoint.Meta
	for rows.Next() {
		var m checkpoint.Meta
		if err := rows.Scan(&m.Version, &m.Label, &m.Timestamp); err != nil {
			return nil, fmt.Errorf("postgres checkpointer: history scan: %w", err)
		}
		metas = append(metas, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres checkpointer: history rows: %w", err)
	}
	return metas, nil
}

// List returns the distinct thread IDs that have stored checkpoints.
//
// This performs a full table scan. See checkpoint.Checkpointer for guidance.
func (c *Checkpointer) List(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(`SELECT DISTINCT thread_id FROM %s`, c.cfg.tableName)

	rows, err := c.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres checkpointer: list: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres checkpointer: list scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres checkpointer: list rows: %w", err)
	}
	return ids, nil
}

// Delete removes every checkpoint for the thread.
func (c *Checkpointer) Delete(ctx context.Context, threadID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE thread_id = $1`, c.cfg.tableName)
	if _, err := c.pool.Exec(ctx, query, threadID); err != nil {
		return fmt.Errorf("postgres checkpointer: delete: %w", err)
	}
	return nil
}

// Close closes the underlying connection pool.
func (c *Checkpointer) Close() error {
	c.pool.Close()
	return nil
}

// row is the subset of pgx.Row this package needs, so tests can supply a fake.
type row interface {
	Scan(dest ...any) error
}

// scanOne materializes a single checkpoint row.
func (c *Checkpointer) scanOne(_ context.Context, threadID string, r row) (checkpoint.Checkpoint, error) {
	cp := checkpoint.Checkpoint{ThreadID: threadID}
	var stateJSON, usageJSON, extraJSON []byte

	if err := r.Scan(&cp.Version, &cp.Label, &stateJSON, &usageJSON, &extraJSON, &cp.Timestamp); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return checkpoint.Checkpoint{}, checkpoint.ErrNotFound
		}
		return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: load: %w", err)
	}

	if len(stateJSON) > 0 {
		if err := json.Unmarshal(stateJSON, &cp.State); err != nil {
			return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: unmarshal state: %w", err)
		}
	}
	if len(usageJSON) > 0 {
		if err := json.Unmarshal(usageJSON, &cp.Usage); err != nil {
			return checkpoint.Checkpoint{}, fmt.Errorf("postgres checkpointer: unmarshal usage: %w", err)
		}
	}
	if len(extraJSON) > 0 {
		cp.Extra = append(json.RawMessage(nil), extraJSON...)
	}

	return cp, nil
}
