// Package postgres provides a PostgreSQL conversation store.
// The caller must create a table with this shape:
//
//	CREATE TABLE conversations (
//	    conversation_id TEXT PRIMARY KEY,
//	    messages        JSONB NOT NULL,
//	    revision        BIGINT NOT NULL,
//	    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ agent.ConversationManager = (*Conversation)(nil)

// Conversation implements agent.ConversationManager using PostgreSQL.
type Conversation struct {
	pool *pgxpool.Pool
	cfg  *pgConfig
}

// New creates a conversation store over a connected pool.
func New(pool *pgxpool.Pool, opts ...Option) (*Conversation, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres conversation: pool is required")
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}
	for _, name := range []string{cfg.tableName, cfg.colID, cfg.colMessages, cfg.colRevision, cfg.colUpdatedAt} {
		if name == "" || strings.ContainsRune(name, 0) {
			return nil, fmt.Errorf("postgres conversation: identifier %q is invalid", name)
		}
	}
	cfg.tableName = pgx.Identifier{cfg.tableName}.Sanitize()
	cfg.colID = pgx.Identifier{cfg.colID}.Sanitize()
	cfg.colMessages = pgx.Identifier{cfg.colMessages}.Sanitize()
	cfg.colRevision = pgx.Identifier{cfg.colRevision}.Sanitize()
	cfg.colUpdatedAt = pgx.Identifier{cfg.colUpdatedAt}.Sanitize()

	// Upgrade existing pre-CAS tables in place. Legacy rows receive revision
	// zero, which permits exactly one expectedRevision=0 CAS update.
	migration := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s BIGINT NOT NULL DEFAULT 0`, cfg.tableName, cfg.colRevision)
	if _, err := pool.Exec(context.Background(), migration); err != nil {
		return nil, fmt.Errorf("postgres conversation: ensure revision column: %w", err)
	}

	return &Conversation{pool: pool, cfg: cfg}, nil
}

// Save atomically persists messages when expectedRevision matches. New
// conversations must use expectedRevision zero.
func (m *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return 0, fmt.Errorf("postgres conversation: marshal: %w", err)
	}

	var query string
	var args []any
	if expectedRevision == 0 {
		query = fmt.Sprintf(`
			INSERT INTO %s (%s, %s, %s, %s)
			VALUES ($1, $2, 1, NOW())
			ON CONFLICT (%s) DO UPDATE SET
				%s = EXCLUDED.%s,
				%s = 1,
				%s = NOW()
			WHERE %s.%s = 0
			RETURNING %s
		`, m.cfg.tableName, m.cfg.colID, m.cfg.colMessages, m.cfg.colRevision,
			m.cfg.colUpdatedAt, m.cfg.colID, m.cfg.colMessages, m.cfg.colMessages,
			m.cfg.colRevision, m.cfg.colUpdatedAt, m.cfg.tableName, m.cfg.colRevision,
			m.cfg.colRevision)
		args = []any{conversationID, data}
	} else {
		query = fmt.Sprintf(`
			UPDATE %s SET
				%s = $2,
				%s = %s + 1,
				%s = NOW()
			WHERE %s = $1 AND %s = $3
			RETURNING %s
		`, m.cfg.tableName, m.cfg.colMessages, m.cfg.colRevision, m.cfg.colRevision,
			m.cfg.colUpdatedAt, m.cfg.colID, m.cfg.colRevision, m.cfg.colRevision)
		args = []any{conversationID, data, expectedRevision}
	}

	var revision uint64
	err = m.pool.QueryRow(ctx, query, args...).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("postgres conversation: save %q: %w", conversationID, agent.ErrConversationConflict)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres conversation: save: %w", err)
	}
	return revision, nil
}

// Load returns a snapshot. Missing conversations have revision zero and a
// non-nil empty message slice.
func (m *Conversation) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	query := fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s = $1`,
		m.cfg.colMessages, m.cfg.colRevision, m.cfg.tableName, m.cfg.colID)

	var data []byte
	var revision uint64
	err := m.pool.QueryRow(ctx, query, conversationID).Scan(&data, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("postgres conversation: load: %w", err)
	}

	messages, err := conversation.UnmarshalMessages(data)
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("postgres conversation: unmarshal: %w", err)
	}
	return agent.ConversationSnapshot{Messages: messages, Revision: revision}, nil
}

// List returns IDs ordered by most recent update.
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s DESC`, m.cfg.colID, m.cfg.tableName, m.cfg.colUpdatedAt)
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

// Delete removes a conversation. Missing conversations are ignored.
func (m *Conversation) Delete(ctx context.Context, conversationID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, m.cfg.tableName, m.cfg.colID)
	if _, err := m.pool.Exec(ctx, query, conversationID); err != nil {
		return fmt.Errorf("postgres conversation: delete: %w", err)
	}
	return nil
}

// Close closes the underlying connection pool.
func (m *Conversation) Close() error { m.pool.Close(); return nil }
