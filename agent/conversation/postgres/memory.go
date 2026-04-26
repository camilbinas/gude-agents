// Package postgres provides a PostgreSQL-based memory driver for the gude-agents
// framework. It stores each conversation as a row in a PostgreSQL table, with
// messages serialized as JSONB.
//
// This is useful for production deployments where you already run PostgreSQL
// and want durable, ACID-compliant conversation storage with full SQL
// queryability. The JSONB column lets you use PostgreSQL JSON operators to
// query into conversation history if needed.
//
// The driver uses github.com/jackc/pgx/v5, the standard pure-Go PostgreSQL
// driver with native PostgreSQL type support.
//
// By default, the table must already exist. Use WithAutoMigrate to have the
// driver create it automatically (useful for development).
//
// Expected table schema:
//
//	CREATE TABLE conversations (
//	    conversation_id TEXT PRIMARY KEY,
//	    messages        JSONB NOT NULL,
//	    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);
//
// Usage:
//
//	pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/mydb")
//	store, err := postgres.New(pool)
//
//	// Auto-create the table (development):
//	store, err := postgres.New(pool, postgres.WithAutoMigrate())
//
// Run:
//
//	POSTGRES_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable" go run ./postgres-memory
package postgres

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time interface checks.
var _ agent.Conversation = (*PostgresMemory)(nil)
var _ conversation.ConversationManager = (*PostgresMemory)(nil)

// PostgresMemory implements agent.Conversation and conversation.ConversationManager using a
// PostgreSQL database. Each conversation is stored as a row with its messages
// serialized as JSONB.
type PostgresMemory struct {
	pool      *pgxpool.Pool
	tableName string
}

// New creates a new PostgresMemory. The pool should be a connected pgxpool.Pool.
//
// By default, the table must already exist with the expected schema. Use
// WithAutoMigrate to create it automatically.
//
// Returns an error if the pool is nil or (with WithAutoMigrate) the schema
// cannot be initialized.
func New(pool *pgxpool.Pool, opts ...Option) (*PostgresMemory, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres memory: pool is required")
	}

	cfg := &pgConfig{
		tableName: "conversations",
	}
	for _, o := range opts {
		o(cfg)
	}

	if cfg.autoMigrate {
		ddl := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				conversation_id TEXT PRIMARY KEY,
				messages        JSONB NOT NULL,
				updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`, cfg.tableName)
		if _, err := pool.Exec(context.Background(), ddl); err != nil {
			return nil, fmt.Errorf("postgres memory: create table: %w", err)
		}
	}

	return &PostgresMemory{
		pool:      pool,
		tableName: cfg.tableName,
	}, nil
}

// Save persists messages for the given conversation ID. Uses an upsert so
// that both new and existing conversations are handled in a single statement.
func (m *PostgresMemory) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return fmt.Errorf("postgres memory: marshal: %w", err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (conversation_id, messages, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (conversation_id) DO UPDATE SET
			messages   = EXCLUDED.messages,
			updated_at = EXCLUDED.updated_at
	`, m.tableName)

	if _, err := m.pool.Exec(ctx, query, conversationID, data); err != nil {
		return fmt.Errorf("postgres memory: save: %w", err)
	}
	return nil
}

// Load retrieves messages for the given conversation ID.
// Returns an empty non-nil slice if the conversation does not exist.
func (m *PostgresMemory) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	query := fmt.Sprintf(`SELECT messages FROM %s WHERE conversation_id = $1`, m.tableName)

	var data []byte
	err := m.pool.QueryRow(ctx, query, conversationID).Scan(&data)
	if err != nil {
		if err == pgx.ErrNoRows {
			return []agent.Message{}, nil
		}
		return nil, fmt.Errorf("postgres memory: load: %w", err)
	}

	messages, err := conversation.UnmarshalMessages(data)
	if err != nil {
		return nil, fmt.Errorf("postgres memory: unmarshal: %w", err)
	}
	return messages, nil
}

// List returns all conversation IDs in the database, ordered by most recently
// updated first.
func (m *PostgresMemory) List(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(`SELECT conversation_id FROM %s ORDER BY updated_at DESC`, m.tableName)

	rows, err := m.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres memory: list: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres memory: list scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres memory: list rows: %w", err)
	}
	return ids, nil
}

// Delete removes a conversation by ID. Returns nil if the conversation
// does not exist.
func (m *PostgresMemory) Delete(ctx context.Context, conversationID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE conversation_id = $1`, m.tableName)

	if _, err := m.pool.Exec(ctx, query, conversationID); err != nil {
		return fmt.Errorf("postgres memory: delete: %w", err)
	}
	return nil
}

// Close closes the underlying connection pool.
func (m *PostgresMemory) Close() {
	m.pool.Close()
}
