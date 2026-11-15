package sqlite

import (
	"errors"

	"github.com/camilbinas/gude-agents/agent"

	sqliteconv "github.com/camilbinas/gude-agents/agent/conversation/sqlite"
)

// Stores holds both durable halves an agent needs, backed by one SQLite file:
// the append-only conversation transcript and the resumable execution state.
type Stores struct {
	Conversations *sqliteconv.Conversation
	Executions    *Store
}

// Open opens both the conversation and execution stores on one SQLite file and
// returns them wired together. Close it when done. WithBusyTimeout and
// WithTableName apply to the executions half; the conversation half uses its
// default table and the same busy timeout. Both halves open the file with WAL,
// a busy timeout and _txlock=immediate, so they can share it concurrently.
func Open(dsn string, opts ...Option) (*Stores, error) {
	if dsn == "" {
		return nil, errors.New("execution/sqlite: dsn is required")
	}
	cfg := config{table: defaultTable, busyTimeout: defaultBusyTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}
	conversations, err := sqliteconv.New(dsn, sqliteconv.WithBusyTimeout(cfg.busyTimeout))
	if err != nil {
		return nil, err
	}
	executions, err := New(dsn, opts...)
	if err != nil {
		_ = conversations.Close()
		return nil, err
	}
	return &Stores{Conversations: conversations, Executions: executions}, nil
}

// Options returns the agent.Options that register both stores, for
// agent.Agent(provider, instructions, stores.Options()...).
func (s *Stores) Options() []agent.Option {
	return []agent.Option{
		agent.WithConversationStore(s.Conversations),
		agent.WithExecutionStore(s.Executions),
	}
}

// Close closes both stores, joining any errors.
func (s *Stores) Close() error {
	if s == nil {
		return nil
	}
	var convErr, execErr error
	if s.Executions != nil {
		execErr = s.Executions.Close()
	}
	if s.Conversations != nil {
		convErr = s.Conversations.Close()
	}
	return errors.Join(execErr, convErr)
}
