// Package sqlite provides a SQLite-based memory driver for the gude-agents framework.
// It stores each conversation as a row in a SQLite database, with messages serialized
// as JSON.
//
// This is useful for CLI tools, local agents, development, and single-machine
// deployments where you want durable, queryable persistence without running an
// external service. Unlike the disk driver, SQLite provides ACID transactions
// and handles concurrent access safely.
//
// The driver uses modernc.org/sqlite, a pure-Go SQLite implementation that
// requires no CGo and cross-compiles cleanly.
//
// Usage:
//
//	store, err := sqlite.New("/tmp/agent-memory.db")
//	// Creates a SQLite database at /tmp/agent-memory.db
//
//	store, err := sqlite.New(":memory:")
//	// Creates an in-memory database (useful for testing)
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"

	_ "modernc.org/sqlite"
)

// Compile-time interface checks.
var _ agent.Memory = (*SQLiteMemory)(nil)
var _ memory.MemoryManager = (*SQLiteMemory)(nil)

// SQLiteMemory implements agent.Memory and memory.MemoryManager using a
// SQLite database. Each conversation is stored as a row with its messages
// serialized as JSON.
type SQLiteMemory struct {
	db        *sql.DB
	tableName string
}

// New creates a new SQLiteMemory. The dsn is a SQLite connection string —
// typically a file path like "/tmp/agent.db" or ":memory:" for an in-memory
// database.
//
// The conversations table is created automatically if it doesn't exist.
// Returns an error if the database cannot be opened or the schema cannot
// be initialized.
func New(dsn string, opts ...Option) (*SQLiteMemory, error) {
	if dsn == "" {
		return nil, fmt.Errorf("sqlite memory: dsn is required")
	}

	cfg := &sqliteConfig{
		tableName:   "conversations",
		busyTimeout: 5 * time.Second,
	}
	for _, o := range opts {
		o(cfg)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite memory: open: %w", err)
	}

	// Set pragmas for reliability and performance.
	pragmas := fmt.Sprintf(`
		PRAGMA journal_mode=WAL;
		PRAGMA busy_timeout=%d;
		PRAGMA foreign_keys=ON;
	`, cfg.busyTimeout.Milliseconds())
	if _, err := db.Exec(pragmas); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite memory: pragmas: %w", err)
	}

	// Create the conversations table if it doesn't exist.
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			conversation_id TEXT PRIMARY KEY,
			messages        TEXT NOT NULL,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`, cfg.tableName)
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite memory: create table: %w", err)
	}

	return &SQLiteMemory{
		db:        db,
		tableName: cfg.tableName,
	}, nil
}

// Save persists messages for the given conversation ID. Uses an upsert so
// that both new and existing conversations are handled in a single statement.
func (m *SQLiteMemory) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	data, err := memory.MarshalMessages(messages)
	if err != nil {
		return fmt.Errorf("sqlite memory: marshal: %w", err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (conversation_id, messages, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(conversation_id) DO UPDATE SET
			messages   = excluded.messages,
			updated_at = excluded.updated_at
	`, m.tableName)

	if _, err := m.db.ExecContext(ctx, query, conversationID, string(data)); err != nil {
		return fmt.Errorf("sqlite memory: save: %w", err)
	}
	return nil
}

// Load retrieves messages for the given conversation ID.
// Returns an empty non-nil slice if the conversation does not exist.
func (m *SQLiteMemory) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	query := fmt.Sprintf(`SELECT messages FROM %s WHERE conversation_id = ?`, m.tableName)

	var data string
	err := m.db.QueryRowContext(ctx, query, conversationID).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return []agent.Message{}, nil
		}
		return nil, fmt.Errorf("sqlite memory: load: %w", err)
	}

	messages, err := memory.UnmarshalMessages([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("sqlite memory: unmarshal: %w", err)
	}
	return messages, nil
}

// List returns all conversation IDs in the database, ordered by most recently
// updated first.
func (m *SQLiteMemory) List(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(`SELECT conversation_id FROM %s ORDER BY updated_at DESC`, m.tableName)

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("sqlite memory: list: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite memory: list scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite memory: list rows: %w", err)
	}
	return ids, nil
}

// Delete removes a conversation by ID. Returns nil if the conversation
// does not exist.
func (m *SQLiteMemory) Delete(ctx context.Context, conversationID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE conversation_id = ?`, m.tableName)

	if _, err := m.db.ExecContext(ctx, query, conversationID); err != nil {
		return fmt.Errorf("sqlite memory: delete: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (m *SQLiteMemory) Close() error {
	return m.db.Close()
}
