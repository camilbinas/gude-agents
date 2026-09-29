// Package sqlite provides a SQLite-backed conversation store.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"

	_ "modernc.org/sqlite"
)

var _ agent.ConversationManager = (*Conversation)(nil)

// Conversation implements agent.ConversationManager using SQLite.
type Conversation struct {
	db        *sql.DB
	tableName string
}

// New creates a SQLite conversation store. The dsn is normally a file path or
// ":memory:". The conversations table is created automatically.
func New(dsn string, opts ...Option) (*Conversation, error) {
	if dsn == "" {
		return nil, fmt.Errorf("sqlite conversation: dsn is required")
	}

	cfg := &sqliteConfig{tableName: "conversations", busyTimeout: 5 * time.Second}
	for _, o := range opts {
		o(cfg)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite conversation: open: %w", err)
	}

	pragmas := fmt.Sprintf(`
		PRAGMA journal_mode=WAL;
		PRAGMA busy_timeout=%d;
		PRAGMA foreign_keys=ON;
	`, cfg.busyTimeout.Milliseconds())
	if _, err := db.Exec(pragmas); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite conversation: pragmas: %w", err)
	}

	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			conversation_id TEXT PRIMARY KEY,
			messages        TEXT NOT NULL,
			revision        INTEGER NOT NULL,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`, cfg.tableName)
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite conversation: create table: %w", err)
	}
	var hasRevision bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name = 'revision')`, cfg.tableName).Scan(&hasRevision); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite conversation: inspect revision column: %w", err)
	}
	if !hasRevision {
		// Existing pre-CAS tables are upgraded in place. Revision zero marks a
		// legacy row and allows exactly one expectedRevision=0 CAS upgrade.
		alter := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN revision INTEGER NOT NULL DEFAULT 0`, cfg.tableName)
		if _, err := db.Exec(alter); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite conversation: add revision column: %w", err)
		}
	}

	return &Conversation{db: db, tableName: cfg.tableName}, nil
}

// Save atomically persists messages when expectedRevision matches the current
// revision. New conversations must use expectedRevision zero.
func (m *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return 0, fmt.Errorf("sqlite conversation: marshal: %w", err)
	}

	var query string
	var args []any
	if expectedRevision == 0 {
		query = fmt.Sprintf(`
			INSERT INTO %s (conversation_id, messages, revision, updated_at)
			VALUES (?, ?, 1, CURRENT_TIMESTAMP)
			ON CONFLICT(conversation_id) DO UPDATE SET
				messages = excluded.messages,
				revision = 1,
				updated_at = CURRENT_TIMESTAMP
			WHERE revision = 0
			RETURNING revision
		`, m.tableName)
		args = []any{conversationID, string(data)}
	} else {
		query = fmt.Sprintf(`
			UPDATE %s SET
				messages = ?, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
			WHERE conversation_id = ? AND revision = ?
			RETURNING revision
		`, m.tableName)
		args = []any{string(data), conversationID, expectedRevision}
	}

	var revision uint64
	err = m.db.QueryRowContext(ctx, query, args...).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("sqlite conversation: save %q: %w", conversationID, agent.ErrConversationConflict)
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite conversation: save: %w", err)
	}
	return revision, nil
}

// Load returns the messages and revision. Missing conversations return a
// non-nil empty message slice and revision zero.
func (m *Conversation) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	query := fmt.Sprintf(`SELECT messages, revision FROM %s WHERE conversation_id = ?`, m.tableName)

	var data string
	var revision uint64
	err := m.db.QueryRowContext(ctx, query, conversationID).Scan(&data, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("sqlite conversation: load: %w", err)
	}

	messages, err := conversation.UnmarshalMessages([]byte(data))
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("sqlite conversation: unmarshal: %w", err)
	}
	return agent.ConversationSnapshot{Messages: messages, Revision: revision}, nil
}

// List returns all conversation IDs ordered by most recent update.
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(`SELECT conversation_id FROM %s ORDER BY updated_at DESC`, m.tableName)
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("sqlite conversation: list: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite conversation: list scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite conversation: list rows: %w", err)
	}
	return ids, nil
}

// Delete removes a conversation. Missing conversations are ignored.
func (m *Conversation) Delete(ctx context.Context, conversationID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE conversation_id = ?`, m.tableName)
	if _, err := m.db.ExecContext(ctx, query, conversationID); err != nil {
		return fmt.Errorf("sqlite conversation: delete: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (m *Conversation) Close() error { return m.db.Close() }
