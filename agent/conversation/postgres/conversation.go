// Package postgres provides an append-only PostgreSQL conversation store.
//
// The metadata table is migrated in-place from the legacy snapshot layout.
// Canonical messages live in a separate (conversation_id, sequence) table;
// normal execution never reads or rewrites the legacy messages JSONB column.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	_ agent.ConversationManager = (*Conversation)(nil)
	_ agent.ContextStateStore   = (*Conversation)(nil)
)

type Conversation struct {
	pool *pgxpool.Pool
	cfg  *pgConfig
}

func New(pool *pgxpool.Pool, opts ...Option) (*Conversation, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres conversation: pool is required")
	}
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}
	for _, name := range []string{cfg.tableName, cfg.messageTable, cfg.colID, cfg.colMessages, cfg.colRevision, cfg.colUpdatedAt} {
		if name == "" || strings.ContainsRune(name, 0) {
			return nil, fmt.Errorf("postgres conversation: identifier %q is invalid", name)
		}
	}
	cfg.tableName = pgx.Identifier{cfg.tableName}.Sanitize()
	cfg.messageTable = pgx.Identifier{cfg.messageTable}.Sanitize()
	cfg.colID = pgx.Identifier{cfg.colID}.Sanitize()
	cfg.colMessages = pgx.Identifier{cfg.colMessages}.Sanitize()
	cfg.colRevision = pgx.Identifier{cfg.colRevision}.Sanitize()
	cfg.colUpdatedAt = pgx.Identifier{cfg.colUpdatedAt}.Sanitize()
	// The caller still owns legacy metadata-table creation. These additive DDL
	// statements migrate it to metadata+append-only message rows.
	for _, sql := range []string{
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s BIGINT NOT NULL DEFAULT 0`, cfg.tableName, cfg.colRevision),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS last_sequence BIGINT NOT NULL DEFAULT 0`, cfg.tableName),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS context_state JSONB NOT NULL DEFAULT '{}'::jsonb`, cfg.tableName),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS context_state_revision BIGINT NOT NULL DEFAULT 0`, cfg.tableName),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (conversation_id TEXT NOT NULL REFERENCES %s(%s) ON DELETE CASCADE, sequence BIGINT NOT NULL, message JSONB NOT NULL, PRIMARY KEY (conversation_id, sequence))`, cfg.messageTable, cfg.tableName, cfg.colID),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(conversation_id, sequence)`, pgx.Identifier{strings.Trim(cfg.messageTable, `"`) + "_range"}.Sanitize(), cfg.messageTable),
	} {
		if _, err := pool.Exec(context.Background(), sql); err != nil {
			return nil, fmt.Errorf("postgres conversation: migrate: %w", err)
		}
	}
	m := &Conversation{pool: pool, cfg: cfg}
	if err := m.migrateLegacy(context.Background()); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Conversation) migrateLegacy(ctx context.Context) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres conversation: begin legacy migration: %w", err)
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s, %s FROM %s WHERE last_sequence = 0 AND %s IS NOT NULL AND %s != '[]'::jsonb FOR UPDATE`, m.cfg.colID, m.cfg.colMessages, m.cfg.tableName, m.cfg.colMessages, m.cfg.colMessages))
	if err != nil {
		return fmt.Errorf("postgres conversation: query legacy: %w", err)
	}
	type legacyConversation struct {
		id   string
		msgs []agent.Message
	}
	legacy := make([]legacyConversation, 0)
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		msgs, err := conversation.UnmarshalMessages(raw)
		if err != nil {
			return fmt.Errorf("postgres conversation: decode legacy %q: %w", id, err)
		}
		legacy = append(legacy, legacyConversation{id: id, msgs: msgs})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// pgx permits only one active result stream on a transaction connection.
	// Drain and close the locked legacy query before issuing migration writes.
	rows.Close()
	for _, row := range legacy {
		for i, msg := range row.msgs {
			if err := m.insertMessage(ctx, tx, row.id, uint64(i+1), msg); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET last_sequence = $1 WHERE %s = $2`, m.cfg.tableName, m.cfg.colID), len(row.msgs), row.id); err != nil {
			return fmt.Errorf("postgres conversation: update legacy metadata: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres conversation: commit legacy migration: %w", err)
	}
	return nil
}

func (m *Conversation) insertMessage(ctx context.Context, tx pgx.Tx, id string, seq uint64, msg agent.Message) error {
	raw, err := conversation.MarshalMessages([]agent.Message{msg})
	if err != nil {
		return fmt.Errorf("postgres conversation: marshal message: %w", err)
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s(conversation_id, sequence, message) VALUES ($1, $2, $3)`, m.cfg.messageTable), id, seq, raw); err != nil {
		return fmt.Errorf("postgres conversation: insert message: %w", err)
	}
	return nil
}

func (m *Conversation) Load(ctx context.Context, id string) (agent.ConversationSnapshot, error) {
	return m.LoadAfter(ctx, id, 0)
}
func (m *Conversation) LoadAfter(ctx context.Context, id string, after uint64) (agent.ConversationSnapshot, error) {
	var rev, last uint64
	err := m.pool.QueryRow(ctx, fmt.Sprintf(`SELECT %s,last_sequence FROM %s WHERE %s=$1`, m.cfg.colRevision, m.cfg.tableName, m.cfg.colID), id).Scan(&rev, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("postgres conversation: load metadata: %w", err)
	}
	rows, err := m.pool.Query(ctx, fmt.Sprintf(`SELECT sequence,message FROM %s WHERE conversation_id=$1 AND sequence>$2 ORDER BY sequence`, m.cfg.messageTable), id, after)
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("postgres conversation: load range: %w", err)
	}
	defer rows.Close()
	msgs := make([]agent.Message, 0)
	for rows.Next() {
		var seq uint64
		var raw []byte
		if err := rows.Scan(&seq, &raw); err != nil {
			return agent.ConversationSnapshot{}, err
		}
		one, err := conversation.UnmarshalMessages(raw)
		if err != nil || len(one) != 1 {
			if err == nil {
				err = errors.New("expected one message")
			}
			return agent.ConversationSnapshot{}, fmt.Errorf("postgres conversation: decode message %d: %w", seq, err)
		}
		msgs = append(msgs, one[0])
	}
	if err := rows.Err(); err != nil {
		return agent.ConversationSnapshot{}, err
	}
	return agent.ConversationSnapshot{Messages: msgs, Revision: rev, LastSequence: last}, nil
}

func (m *Conversation) Append(ctx context.Context, id string, msgs []agent.Message, expected uint64) (agent.ConversationCursor, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("postgres conversation: begin append: %w", err)
	}
	defer tx.Rollback(ctx)
	var rev, last uint64
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT %s,last_sequence FROM %s WHERE %s=$1 FOR UPDATE`, m.cfg.colRevision, m.cfg.tableName, m.cfg.colID), id).Scan(&rev, &last)
	missing := errors.Is(err, pgx.ErrNoRows)
	if missing {
		rev, last = 0, 0
	} else if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("postgres conversation: append metadata: %w", err)
	}
	if rev != expected {
		return agent.ConversationCursor{}, fmt.Errorf("postgres conversation: append %q: %w", id, agent.ErrConversationConflict)
	}
	if len(msgs) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return agent.ConversationCursor{}, err
		}
		return agent.ConversationCursor{Revision: rev, LastSequence: last}, nil
	}
	if missing {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s(%s,%s,last_sequence,context_state,context_state_revision,%s) VALUES ($1,0,0,'{}'::jsonb,0,NOW()) ON CONFLICT (%s) DO NOTHING`, m.cfg.tableName, m.cfg.colID, m.cfg.colRevision, m.cfg.colUpdatedAt, m.cfg.colID), id); err != nil {
			return agent.ConversationCursor{}, fmt.Errorf("postgres conversation: create metadata: %w", err)
		}
	}
	for i, msg := range msgs {
		if err := m.insertMessage(ctx, tx, id, last+uint64(i)+1, msg); err != nil {
			return agent.ConversationCursor{}, err
		}
	}
	next, nextLast := rev+1, last+uint64(len(msgs))
	tag, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s=$1,last_sequence=$2,%s=NOW() WHERE %s=$3 AND %s=$4`, m.cfg.tableName, m.cfg.colRevision, m.cfg.colUpdatedAt, m.cfg.colID, m.cfg.colRevision), next, nextLast, id, rev)
	if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("postgres conversation: update metadata: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return agent.ConversationCursor{}, fmt.Errorf("postgres conversation: append %q: %w", id, agent.ErrConversationConflict)
	}
	if err := tx.Commit(ctx); err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("postgres conversation: commit append: %w", err)
	}
	return agent.ConversationCursor{Revision: next, LastSequence: nextLast}, nil
}

type stateEnvelope map[string]struct {
	Data     json.RawMessage `json:"data"`
	Revision uint64          `json:"revision"`
}

func (m *Conversation) LoadContextState(ctx context.Context, id, key string) (agent.ContextStateSnapshot, error) {
	var raw []byte
	err := m.pool.QueryRow(ctx, fmt.Sprintf(`SELECT context_state FROM %s WHERE %s=$1`, m.cfg.tableName, m.cfg.colID), id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.ContextStateSnapshot{}, nil
	}
	if err != nil {
		return agent.ContextStateSnapshot{}, fmt.Errorf("postgres conversation: load context state: %w", err)
	}
	var states stateEnvelope
	if err := json.Unmarshal(raw, &states); err != nil {
		return agent.ContextStateSnapshot{}, err
	}
	v := states[key]
	return agent.ContextStateSnapshot{Data: append(json.RawMessage(nil), v.Data...), Revision: v.Revision}, nil
}
func (m *Conversation) SaveContextState(ctx context.Context, id, key string, data json.RawMessage, expected uint64) (uint64, error) {
	if !json.Valid(data) {
		return 0, fmt.Errorf("postgres conversation: context state %q is invalid JSON", key)
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT context_state FROM %s WHERE %s=$1 FOR UPDATE`, m.cfg.tableName, m.cfg.colID), id).Scan(&raw)
	missing := errors.Is(err, pgx.ErrNoRows)
	if missing {
		raw = []byte("{}")
	} else if err != nil {
		return 0, err
	}
	var states stateEnvelope
	if err := json.Unmarshal(raw, &states); err != nil {
		return 0, err
	}
	if states == nil {
		states = make(stateEnvelope)
	}
	v := states[key]
	if v.Revision != expected {
		return 0, fmt.Errorf("postgres conversation: context state %q: %w", key, agent.ErrContextStateConflict)
	}
	v.Revision++
	v.Data = append(json.RawMessage(nil), data...)
	states[key] = v
	encoded, _ := json.Marshal(states)
	if missing {
		_, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s(%s,%s,last_sequence,context_state,context_state_revision,%s) VALUES($1,0,0,$2,1,NOW()) ON CONFLICT (%s) DO UPDATE SET context_state=EXCLUDED.context_state,context_state_revision=%s.context_state_revision+1`, m.cfg.tableName, m.cfg.colID, m.cfg.colRevision, m.cfg.colUpdatedAt, m.cfg.colID, m.cfg.tableName), id, encoded)
	} else {
		_, err = tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET context_state=$1,context_state_revision=context_state_revision+1 WHERE %s=$2`, m.cfg.tableName, m.cfg.colID), encoded, id)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres conversation: save context state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return v.Revision, nil
}
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	rows, err := m.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE %s>0 ORDER BY %s DESC`, m.cfg.colID, m.cfg.tableName, m.cfg.colRevision, m.cfg.colUpdatedAt))
	if err != nil {
		return nil, fmt.Errorf("postgres conversation: list: %w", err)
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
	if _, err := m.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s=$1`, m.cfg.tableName, m.cfg.colID), id); err != nil {
		return fmt.Errorf("postgres conversation: delete: %w", err)
	}
	return nil
}
func (m *Conversation) Close() error { m.pool.Close(); return nil }
