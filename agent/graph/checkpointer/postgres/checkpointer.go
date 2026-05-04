// Package postgres provides a PostgreSQL-backed implementation of the
// GraphCheckpointer interface for persistent graph execution checkpointing.
//
// # Table Schema
//
// The table must be created by the caller. Expected schema:
//
//	CREATE TABLE graph_checkpoints (
//	    thread_id   TEXT NOT NULL,
//	    version     INTEGER NOT NULL,
//	    node_name   TEXT NOT NULL,
//	    state       JSONB NOT NULL,
//	    metadata    JSONB NOT NULL,
//	    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
//	    PRIMARY KEY (thread_id, version)
//	);
//
// # Usage
//
//	cp, err := postgres.New(pool)
//	cp, err := postgres.New(pool, postgres.WithTableName("my_checkpoints"))
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time interface check.
var _ graph.GraphCheckpointer = (*Checkpointer)(nil)

// Checkpointer implements graph.GraphCheckpointer using PostgreSQL.
type Checkpointer struct {
	pool *pgxpool.Pool
	cfg  *pgConfig
}

// checkpointMetadata holds the non-state fields stored in the metadata JSONB column.
type checkpointMetadata struct {
	Completed  map[string]bool `json:"completed,omitempty"`
	Iterations int             `json:"iterations"`
	Usage      json.RawMessage `json:"usage,omitempty"`
}

// New creates a new Postgres Checkpointer. The pool should be a connected pgxpool.Pool.
// The table must already exist with the expected schema.
//
// Returns an error if the pool is nil.
func New(pool *pgxpool.Pool, opts ...Option) (*Checkpointer, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres checkpointer: pool is required")
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	// Validate and sanitize identifier for safe SQL interpolation.
	if cfg.tableName == "" || strings.ContainsRune(cfg.tableName, 0) {
		return nil, fmt.Errorf("postgres checkpointer: table name %q is invalid", cfg.tableName)
	}
	cfg.tableName = pgx.Identifier{cfg.tableName}.Sanitize()

	return &Checkpointer{
		pool: pool,
		cfg:  cfg,
	}, nil
}

// Save persists a checkpoint for the given thread. It determines the next version
// by querying the current max version and assigns version = max + 1.
// This is append-only: a single INSERT per save, no updates.
func (c *Checkpointer) Save(ctx context.Context, threadID string, cp graph.Checkpoint) (graph.Checkpoint, error) {
	// Determine next version.
	var maxVersion int
	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(version), 0) FROM %s WHERE thread_id = $1`,
		c.cfg.tableName,
	)
	if err := c.pool.QueryRow(ctx, query, threadID).Scan(&maxVersion); err != nil {
		return graph.Checkpoint{}, fmt.Errorf("postgres checkpointer: save max version: %w", err)
	}

	version := maxVersion + 1
	cp.ThreadID = threadID
	cp.Version = version
	if cp.Timestamp.IsZero() {
		cp.Timestamp = time.Now()
	}

	// Marshal state.
	stateJSON, err := json.Marshal(cp.State)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("postgres checkpointer: save marshal state: %w", err)
	}

	// Marshal metadata (completed, iterations, usage).
	usageJSON, err := json.Marshal(cp.Usage)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("postgres checkpointer: save marshal usage: %w", err)
	}
	meta := checkpointMetadata{
		Completed:  cp.Completed,
		Iterations: cp.Iterations,
		Usage:      usageJSON,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("postgres checkpointer: save marshal metadata: %w", err)
	}

	insertQuery := fmt.Sprintf(
		`INSERT INTO %s (thread_id, version, node_name, state, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		c.cfg.tableName,
	)
	_, err = c.pool.Exec(ctx, insertQuery,
		threadID, version, cp.NodeName, stateJSON, metaJSON, cp.Timestamp,
	)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("postgres checkpointer: save: %w", err)
	}

	return cp, nil
}

// Load retrieves the latest checkpoint (highest version) for the given thread.
// Returns graph.ErrCheckpointNotFound if no checkpoints exist.
func (c *Checkpointer) Load(ctx context.Context, threadID string) (graph.Checkpoint, error) {
	query := fmt.Sprintf(
		`SELECT version, node_name, state, metadata, created_at
		 FROM %s WHERE thread_id = $1
		 ORDER BY version DESC LIMIT 1`,
		c.cfg.tableName,
	)

	return c.scanCheckpoint(ctx, query, threadID)
}

// LoadAt retrieves the checkpoint at a specific version for the given thread.
// Returns graph.ErrCheckpointNotFound if the version does not exist.
func (c *Checkpointer) LoadAt(ctx context.Context, threadID string, version int) (graph.Checkpoint, error) {
	query := fmt.Sprintf(
		`SELECT version, node_name, state, metadata, created_at
		 FROM %s WHERE thread_id = $1 AND version = $2`,
		c.cfg.tableName,
	)

	row := c.pool.QueryRow(ctx, query, threadID, version)

	var cp graph.Checkpoint
	var stateJSON, metaJSON []byte
	cp.ThreadID = threadID

	err := row.Scan(&cp.Version, &cp.NodeName, &stateJSON, &metaJSON, &cp.Timestamp)
	if err != nil {
		if err == pgx.ErrNoRows {
			return graph.Checkpoint{}, graph.ErrCheckpointNotFound
		}
		return graph.Checkpoint{}, fmt.Errorf("postgres checkpointer: load at: %w", err)
	}

	if err := c.hydrateCheckpoint(&cp, stateJSON, metaJSON); err != nil {
		return graph.Checkpoint{}, err
	}
	return cp, nil
}

// History returns ordered checkpoint metadata for a thread (oldest first).
func (c *Checkpointer) History(ctx context.Context, threadID string) ([]graph.CheckpointMeta, error) {
	query := fmt.Sprintf(
		`SELECT version, node_name, created_at
		 FROM %s WHERE thread_id = $1
		 ORDER BY version ASC`,
		c.cfg.tableName,
	)

	rows, err := c.pool.Query(ctx, query, threadID)
	if err != nil {
		return nil, fmt.Errorf("postgres checkpointer: history: %w", err)
	}
	defer rows.Close()

	var metas []graph.CheckpointMeta
	for rows.Next() {
		var m graph.CheckpointMeta
		if err := rows.Scan(&m.Version, &m.NodeName, &m.Timestamp); err != nil {
			return nil, fmt.Errorf("postgres checkpointer: history scan: %w", err)
		}
		metas = append(metas, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres checkpointer: history rows: %w", err)
	}
	return metas, nil
}

// List returns all distinct thread IDs that have stored checkpoints.
func (c *Checkpointer) List(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(
		`SELECT DISTINCT thread_id FROM %s`,
		c.cfg.tableName,
	)

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

// Delete removes all checkpoints for a thread.
func (c *Checkpointer) Delete(ctx context.Context, threadID string) error {
	query := fmt.Sprintf(
		`DELETE FROM %s WHERE thread_id = $1`,
		c.cfg.tableName,
	)

	_, err := c.pool.Exec(ctx, query, threadID)
	if err != nil {
		return fmt.Errorf("postgres checkpointer: delete: %w", err)
	}
	return nil
}

// Close closes the underlying connection pool.
func (c *Checkpointer) Close() error {
	c.pool.Close()
	return nil
}

// scanCheckpoint scans a single checkpoint row from a query with one string parameter.
func (c *Checkpointer) scanCheckpoint(ctx context.Context, query string, threadID string) (graph.Checkpoint, error) {
	row := c.pool.QueryRow(ctx, query, threadID)

	var cp graph.Checkpoint
	var stateJSON, metaJSON []byte
	cp.ThreadID = threadID

	err := row.Scan(&cp.Version, &cp.NodeName, &stateJSON, &metaJSON, &cp.Timestamp)
	if err != nil {
		if err == pgx.ErrNoRows {
			return graph.Checkpoint{}, graph.ErrCheckpointNotFound
		}
		return graph.Checkpoint{}, fmt.Errorf("postgres checkpointer: load: %w", err)
	}

	if err := c.hydrateCheckpoint(&cp, stateJSON, metaJSON); err != nil {
		return graph.Checkpoint{}, err
	}
	return cp, nil
}

// hydrateCheckpoint unmarshals state and metadata JSON into a Checkpoint.
func (c *Checkpointer) hydrateCheckpoint(cp *graph.Checkpoint, stateJSON, metaJSON []byte) error {
	if err := json.Unmarshal(stateJSON, &cp.State); err != nil {
		return fmt.Errorf("postgres checkpointer: unmarshal state: %w", err)
	}

	var meta checkpointMetadata
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return fmt.Errorf("postgres checkpointer: unmarshal metadata: %w", err)
	}

	cp.Completed = meta.Completed
	cp.Iterations = meta.Iterations
	if meta.Usage != nil {
		if err := json.Unmarshal(meta.Usage, &cp.Usage); err != nil {
			return fmt.Errorf("postgres checkpointer: unmarshal usage: %w", err)
		}
	}
	return nil
}
