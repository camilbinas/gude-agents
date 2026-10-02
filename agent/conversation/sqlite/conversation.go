// Package sqlite provides an append-only SQLite-backed conversation store.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"

	_ "modernc.org/sqlite"
)

var (
	_ agent.ConversationManager = (*Conversation)(nil)
	_ agent.ContextStateStore   = (*Conversation)(nil)
)

type Conversation struct {
	db           *sql.DB
	tableName    string
	messagesName string
}

func sqliteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// New creates and migrates an append-only SQLite conversation store. Existing
// snapshot rows are copied once into sequential message rows; the old messages
// column is retained only for migration compatibility and is never read after
// construction.
func New(dsn string, opts ...Option) (*Conversation, error) {
	if dsn == "" {
		return nil, fmt.Errorf("sqlite conversation: dsn is required")
	}
	cfg := &sqliteConfig{tableName: "conversations", busyTimeout: 5 * time.Second}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.tableName == "" || strings.ContainsRune(cfg.tableName, 0) {
		return nil, fmt.Errorf("sqlite conversation: identifier %q is invalid", cfg.tableName)
	}
	rawTable := cfg.tableName
	table := sqliteIdentifier(rawTable)
	messages := sqliteIdentifier(rawTable + "_messages")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite conversation: open: %w", err)
	}
	closeOnErr := func(err error) (*Conversation, error) { _ = db.Close(); return nil, err }
	if _, err := db.Exec(fmt.Sprintf("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=%d; PRAGMA foreign_keys=ON;", cfg.busyTimeout.Milliseconds())); err != nil {
		return closeOnErr(fmt.Errorf("sqlite conversation: pragmas: %w", err))
	}
	// Keep legacy messages TEXT nullable: old callers may have created the
	// snapshot layout. Runtime reads and writes exclusively use message rows.
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		conversation_id TEXT PRIMARY KEY,
		messages TEXT,
		revision INTEGER NOT NULL DEFAULT 0,
		last_sequence INTEGER NOT NULL DEFAULT 0,
		context_state TEXT NOT NULL DEFAULT '{}',
		context_state_revision INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`, table)); err != nil {
		return closeOnErr(fmt.Errorf("sqlite conversation: create metadata: %w", err))
	}
	for _, column := range []struct{ name, ddl string }{
		{"revision", "INTEGER NOT NULL DEFAULT 0"}, {"last_sequence", "INTEGER NOT NULL DEFAULT 0"},
		{"context_state", "TEXT NOT NULL DEFAULT '{}'"}, {"context_state_revision", "INTEGER NOT NULL DEFAULT 0"},
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name = ?)`, rawTable, column.name).Scan(&exists); err != nil {
			return closeOnErr(fmt.Errorf("sqlite conversation: inspect %s: %w", column.name, err))
		}
		if !exists {
			if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, sqliteIdentifier(column.name), column.ddl)); err != nil {
				return closeOnErr(fmt.Errorf("sqlite conversation: add %s: %w", column.name, err))
			}
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		conversation_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		message TEXT NOT NULL,
		PRIMARY KEY (conversation_id, sequence),
		FOREIGN KEY (conversation_id) REFERENCES %s(conversation_id) ON DELETE CASCADE
	)`, messages, table)); err != nil {
		return closeOnErr(fmt.Errorf("sqlite conversation: create messages: %w", err))
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(conversation_id, sequence)`, sqliteIdentifier(rawTable+"_messages_range"), messages)); err != nil {
		return closeOnErr(fmt.Errorf("sqlite conversation: create range index: %w", err))
	}
	m := &Conversation{db: db, tableName: table, messagesName: messages}
	if err := m.migrateLegacy(context.Background()); err != nil {
		return closeOnErr(err)
	}
	return m, nil
}

func (m *Conversation) migrateLegacy(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite conversation: begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT conversation_id, messages FROM %s WHERE last_sequence = 0 AND messages IS NOT NULL AND messages != '[]'`, m.tableName))
	if err != nil {
		return fmt.Errorf("sqlite conversation: legacy scan: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("sqlite conversation: legacy row: %w", err)
		}
		msgs, err := conversation.UnmarshalMessages([]byte(raw))
		if err != nil {
			return fmt.Errorf("sqlite conversation: decode legacy %q: %w", id, err)
		}
		for i, msg := range msgs {
			if err := m.insertMessage(ctx, tx, id, uint64(i+1), msg); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET last_sequence = ? WHERE conversation_id = ?`, m.tableName), len(msgs), id); err != nil {
			return fmt.Errorf("sqlite conversation: migrate metadata: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite conversation: legacy rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite conversation: commit migration: %w", err)
	}
	return nil
}

func (m *Conversation) insertMessage(ctx context.Context, tx *sql.Tx, id string, sequence uint64, message agent.Message) error {
	raw, err := conversation.MarshalMessages([]agent.Message{message})
	if err != nil {
		return fmt.Errorf("sqlite conversation: marshal message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(conversation_id, sequence, message) VALUES (?, ?, ?)`, m.messagesName), id, sequence, string(raw)); err != nil {
		return fmt.Errorf("sqlite conversation: insert message: %w", err)
	}
	return nil
}

func (m *Conversation) Load(ctx context.Context, id string) (agent.ConversationSnapshot, error) {
	return m.LoadAfter(ctx, id, 0)
}

func (m *Conversation) LoadAfter(ctx context.Context, id string, after uint64) (agent.ConversationSnapshot, error) {
	var revision, last uint64
	err := m.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT revision, last_sequence FROM %s WHERE conversation_id = ?`, m.tableName), id).Scan(&revision, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("sqlite conversation: load metadata: %w", err)
	}
	rows, err := m.db.QueryContext(ctx, fmt.Sprintf(`SELECT sequence, message FROM %s WHERE conversation_id = ? AND sequence > ? ORDER BY sequence`, m.messagesName), id, after)
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("sqlite conversation: load range: %w", err)
	}
	defer rows.Close()
	messages := make([]agent.Message, 0)
	for rows.Next() {
		var seq uint64
		var raw string
		if err := rows.Scan(&seq, &raw); err != nil {
			return agent.ConversationSnapshot{}, fmt.Errorf("sqlite conversation: scan message: %w", err)
		}
		decoded, err := conversation.UnmarshalMessages([]byte(raw))
		if err != nil || len(decoded) != 1 {
			if err == nil {
				err = errors.New("expected one message")
			}
			return agent.ConversationSnapshot{}, fmt.Errorf("sqlite conversation: decode message %d: %w", seq, err)
		}
		messages = append(messages, decoded[0])
	}
	if err := rows.Err(); err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("sqlite conversation: range rows: %w", err)
	}
	return agent.ConversationSnapshot{Messages: messages, Revision: revision, LastSequence: last}, nil
}

func (m *Conversation) Append(ctx context.Context, id string, messages []agent.Message, expected uint64) (agent.ConversationCursor, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: begin append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var revision, last uint64
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT revision, last_sequence FROM %s WHERE conversation_id = ?`, m.tableName), id).Scan(&revision, &last)
	if errors.Is(err, sql.ErrNoRows) {
		revision, last = 0, 0
	} else if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: append metadata: %w", err)
	}
	if revision != expected {
		return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: append %q: %w", id, agent.ErrConversationConflict)
	}
	if len(messages) == 0 {
		if err := tx.Commit(); err != nil {
			return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: empty append commit: %w", err)
		}
		return agent.ConversationCursor{Revision: revision, LastSequence: last}, nil
	}
	if revision == 0 && last == 0 {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(conversation_id, revision, last_sequence, context_state, context_state_revision, updated_at) VALUES (?, 0, 0, '{}', 0, CURRENT_TIMESTAMP) ON CONFLICT(conversation_id) DO NOTHING`, m.tableName), id); err != nil {
			return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: create metadata: %w", err)
		}
		// A concurrently created row cannot pass the expected zero CAS below.
	}
	for i, message := range messages {
		if err := m.insertMessage(ctx, tx, id, last+uint64(i)+1, message); err != nil {
			return agent.ConversationCursor{}, err
		}
	}
	next := revision + 1
	nextLast := last + uint64(len(messages))
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET revision = ?, last_sequence = ?, updated_at = CURRENT_TIMESTAMP WHERE conversation_id = ? AND revision = ?`, m.tableName), next, nextLast, id, revision)
	if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: update metadata: %w", err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: append %q: %w", id, agent.ErrConversationConflict)
	}
	if err := tx.Commit(); err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("sqlite conversation: commit append: %w", err)
	}
	return agent.ConversationCursor{Revision: next, LastSequence: nextLast}, nil
}

type stateEnvelope map[string]struct {
	Data     json.RawMessage `json:"data"`
	Revision uint64          `json:"revision"`
}

func (m *Conversation) LoadContextState(ctx context.Context, id, key string) (agent.ContextStateSnapshot, error) {
	var raw string
	err := m.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT context_state FROM %s WHERE conversation_id = ?`, m.tableName), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ContextStateSnapshot{}, nil
	}
	if err != nil {
		return agent.ContextStateSnapshot{}, fmt.Errorf("sqlite conversation: load context state: %w", err)
	}
	var state stateEnvelope
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return agent.ContextStateSnapshot{}, fmt.Errorf("sqlite conversation: decode context state: %w", err)
	}
	v := state[key]
	return agent.ContextStateSnapshot{Data: append(json.RawMessage(nil), v.Data...), Revision: v.Revision}, nil
}
func (m *Conversation) SaveContextState(ctx context.Context, id, key string, data json.RawMessage, expected uint64) (uint64, error) {
	if !json.Valid(data) {
		return 0, fmt.Errorf("sqlite conversation: context state %q is invalid JSON", key)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlite conversation: begin context state: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT context_state FROM %s WHERE conversation_id = ?`, m.tableName), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		raw = "{}"
	} else if err != nil {
		return 0, fmt.Errorf("sqlite conversation: context metadata: %w", err)
	}
	var state stateEnvelope
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return 0, fmt.Errorf("sqlite conversation: decode context state: %w", err)
	}
	if state == nil {
		state = make(stateEnvelope)
	}
	current := state[key]
	if current.Revision != expected {
		return 0, fmt.Errorf("sqlite conversation: context state %q: %w", key, agent.ErrContextStateConflict)
	}
	current.Revision++
	current.Data = append(json.RawMessage(nil), data...)
	state[key] = current
	encoded, _ := json.Marshal(state)
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(conversation_id, revision, last_sequence, context_state, context_state_revision, updated_at) VALUES (?, 0, 0, ?, 1, CURRENT_TIMESTAMP) ON CONFLICT(conversation_id) DO UPDATE SET context_state = excluded.context_state, context_state_revision = %s.context_state_revision + 1`, m.tableName, m.tableName), id, string(encoded)); err != nil {
		return 0, fmt.Errorf("sqlite conversation: save context state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlite conversation: commit context state: %w", err)
	}
	return current.Revision, nil
}

func (m *Conversation) List(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, fmt.Sprintf(`SELECT conversation_id FROM %s WHERE revision > 0 ORDER BY updated_at DESC`, m.tableName))
	if err != nil {
		return nil, fmt.Errorf("sqlite conversation: list: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (m *Conversation) Delete(ctx context.Context, id string) error {
	if _, err := m.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE conversation_id = ?`, m.tableName), id); err != nil {
		return fmt.Errorf("sqlite conversation: delete: %w", err)
	}
	return nil
}
func (m *Conversation) Close() error { return m.db.Close() }
