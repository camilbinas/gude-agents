// Package postgres provides a PostgreSQL-based conversation driver.
// The table must be created by the caller. Expected schema:
//
//	CREATE TABLE conversations (
//	    conversation_id TEXT PRIMARY KEY,
//	    messages        JSONB NOT NULL,
//	    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);
//
// Usage:
//
//	store, err := postgres.New(pool)
//	store, err := postgres.New(pool,
//	    postgres.WithTableName("chat_history"),
//	    postgres.WithColumns("id", "data", "modified_at"),
//	)
package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time interface checks.
var _ agent.Conversation = (*Conversation)(nil)

// Conversation implements agent.Conversation using PostgreSQL.
type Conversation struct {
	pool *pgxpool.Pool
	cfg  *pgConfig
}

// New creates a new Conversation. The pool should be a connected pgxpool.Pool.
// The table must already exist with the expected schema.
//
// Returns an error if the pool is nil.
func New(pool *pgxpool.Pool, opts ...Option) (*Conversation, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres conversation: pool is required")
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	// Validate and sanitize identifiers for safe SQL interpolation.
	for _, name := range []string{cfg.tableName, cfg.colID, cfg.colMessages, cfg.colUpdatedAt} {
		if name == "" || strings.ContainsRune(name, 0) {
			return nil, fmt.Errorf("postgres conversation: identifier %q is invalid", name)
		}
	}
	cfg.tableName = pgx.Identifier{cfg.tableName}.Sanitize()
	cfg.colID = pgx.Identifier{cfg.colID}.Sanitize()
	cfg.colMessages = pgx.Identifier{cfg.colMessages}.Sanitize()
	cfg.colUpdatedAt = pgx.Identifier{cfg.colUpdatedAt}.Sanitize()

	return &Conversation{
		pool: pool,
		cfg:  cfg,
	}, nil
}

// Save persists messages for the given conversation ID. Uses an upsert so
// that both new and existing conversations are handled in a single statement.
func (m *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return fmt.Errorf("postgres conversation: marshal: %w", err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (%s, %s, %s)
		VALUES ($1, $2, NOW())
		ON CONFLICT (%s) DO UPDATE SET
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s
	`, m.cfg.tableName,
		m.cfg.colID, m.cfg.colMessages, m.cfg.colUpdatedAt,
		m.cfg.colID,
		m.cfg.colMessages, m.cfg.colMessages,
		m.cfg.colUpdatedAt, m.cfg.colUpdatedAt,
	)

	if _, err := m.pool.Exec(ctx, query, conversationID, data); err != nil {
		return fmt.Errorf("postgres conversation: save: %w", err)
	}
	return nil
}

// Load retrieves messages for the given conversation ID.
// Returns an empty non-nil slice if the conversation does not exist.
func (m *Conversation) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE %s = $1`,
		m.cfg.colMessages, m.cfg.tableName, m.cfg.colID)

	var data []byte
	err := m.pool.QueryRow(ctx, query, conversationID).Scan(&data)
	if err != nil {
		if err == pgx.ErrNoRows {
			return []agent.Message{}, nil
		}
		return nil, fmt.Errorf("postgres conversation: load: %w", err)
	}

	messages, err := conversation.UnmarshalMessages(data)
	if err != nil {
		return nil, fmt.Errorf("postgres conversation: unmarshal: %w", err)
	}
	return messages, nil
}

// List returns all conversation IDs in the database, ordered by most recently
// updated first.
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s DESC`,
		m.cfg.colID, m.cfg.tableName, m.cfg.colUpdatedAt)

	rows, err := m.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres conversation: list: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres conversation: list scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres conversation: list rows: %w", err)
	}
	return ids, nil
}

// Delete removes a conversation by ID. Returns nil if the conversation
// does not exist.
func (m *Conversation) Delete(ctx context.Context, conversationID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`,
		m.cfg.tableName, m.cfg.colID)

	if _, err := m.pool.Exec(ctx, query, conversationID); err != nil {
		return fmt.Errorf("postgres conversation: delete: %w", err)
	}
	return nil
}

// Close closes the underlying connection pool.
func (m *Conversation) Close() error {
	m.pool.Close()
	return nil
}
