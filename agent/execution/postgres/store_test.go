package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/conformance"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testTable = "gude_execution_test"

const testDDL = `CREATE TABLE IF NOT EXISTS %s (
    execution_id TEXT        PRIMARY KEY,
    version      BIGINT      NOT NULL CHECK (version > 0),
    payload      JSON        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

// newPool connects to POSTGRES_URL, creates the table, and truncates it so each
// test starts clean. Without POSTGRES_URL the live tests are skipped; the
// configuration and error-mapping tests below always run.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set, skipping postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("postgres unreachable at POSTGRES_URL: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(testDDL, testTable)); err != nil {
		pool.Close()
		t.Fatalf("create table: %v", err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE "+testTable); err != nil {
		pool.Close()
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestConformance(t *testing.T) {
	pool := newPool(t)
	conformance.Run(t, func(t *testing.T) agent.ExecutionStore {
		return New(pool, WithTableName(testTable))
	})
}

func TestAgentProtocol(t *testing.T) {
	pool := newPool(t)
	conformance.RunAgent(t, func(t *testing.T) agent.ExecutionStore {
		return New(pool, WithTableName(testTable))
	})
}

func TestPayloadIsQueryableJSON(t *testing.T) {
	pool := newPool(t)
	store := New(pool, WithTableName(testTable))
	ctx := context.Background()
	if _, err := store.Create(ctx, conformance.Full("sql-query")); err != nil {
		t.Fatal(err)
	}
	var status, phase string
	var version int64
	err := pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT payload->>'status', payload->>'phase', version FROM %s WHERE execution_id = $1`, testTable), "sql-query").
		Scan(&status, &phase, &version)
	if err != nil {
		t.Fatal(err)
	}
	if status != string(agent.ExecutionPaused) || phase != string(agent.ExecutionPhasePaused) || version != 1 {
		t.Fatalf("row = status %q phase %q version %d", status, phase, version)
	}
	var payloadVersion string
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT payload->>'version' FROM %s WHERE execution_id = $1`, testTable), "sql-query").Scan(&payloadVersion); err != nil {
		t.Fatal(err)
	}
	if payloadVersion != "0" {
		t.Fatalf("payload version = %q; the version column is authoritative", payloadVersion)
	}
}

// fakeDB scripts the three statements the store issues so error mapping can be
// verified without a server. SQL semantics are covered by the live tests.
type fakeDB struct {
	execTag pgconn.CommandTag
	execErr error
	rowErr  error
	rows    func(dest ...any) error
}

func (f *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return f.execTag, f.execErr
}

func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row { return fakeRow{f} }

type fakeRow struct{ db *fakeDB }

func (r fakeRow) Scan(dest ...any) error {
	if r.db.rowErr != nil {
		return r.db.rowErr
	}
	return r.db.rows(dest...)
}

func TestSQLOutcomesMapToContractErrors(t *testing.T) {
	ctx := context.Background()
	exec := conformance.Full("exec-map")

	t.Run("create with no inserted row is a conflict", func(t *testing.T) {
		store := New(&fakeDB{execTag: pgconn.NewCommandTag("INSERT 0 0")})
		if _, err := store.Create(ctx, exec); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("Create = %v, want ErrExecutionConflict", err)
		}
	})
	t.Run("create database error is not a conflict", func(t *testing.T) {
		store := New(&fakeDB{execErr: errors.New("connection reset")})
		if _, err := store.Create(ctx, exec); err == nil || errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("Create = %v, want a non-conflict error", err)
		}
	})
	t.Run("load without a row is not found", func(t *testing.T) {
		store := New(&fakeDB{rowErr: pgx.ErrNoRows})
		if _, err := store.Load(ctx, "exec-map"); !errors.Is(err, agent.ErrExecutionNotFound) {
			t.Fatalf("Load = %v, want ErrExecutionNotFound", err)
		}
	})
	t.Run("load database error is not not-found", func(t *testing.T) {
		store := New(&fakeDB{rowErr: errors.New("connection reset")})
		if _, err := store.Load(ctx, "exec-map"); err == nil || errors.Is(err, agent.ErrExecutionNotFound) {
			t.Fatalf("Load = %v, want a non-not-found error", err)
		}
	})
	t.Run("save matching no row is a conflict", func(t *testing.T) {
		store := New(&fakeDB{rowErr: pgx.ErrNoRows})
		if _, err := store.Save(ctx, exec, 3); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("Save = %v, want ErrExecutionConflict", err)
		}
	})
	t.Run("save database error is not a conflict", func(t *testing.T) {
		store := New(&fakeDB{rowErr: errors.New("connection reset")})
		if _, err := store.Save(ctx, exec, 3); err == nil || errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("Save = %v, want a non-conflict error", err)
		}
	})
	t.Run("save returns the durable version from the database", func(t *testing.T) {
		store := New(&fakeDB{rows: func(dest ...any) error {
			*(dest[0].(*int64)) = 8
			return nil
		}})
		saved, err := store.Save(ctx, exec, 7)
		if err != nil || saved.Version != 8 {
			t.Fatalf("Save = version %d, %v; want the version RETURNING reported", saved.Version, err)
		}
	})
	t.Run("a non-positive stored version is rejected", func(t *testing.T) {
		store := New(&fakeDB{rows: func(dest ...any) error {
			*(dest[0].(*int64)) = 0
			return nil
		}})
		if _, err := store.Save(ctx, exec, 7); err == nil {
			t.Fatal("Save accepted version 0 from the database")
		}
	})
	t.Run("a zero expected version never reaches the database", func(t *testing.T) {
		store := New(&fakeDB{rowErr: errors.New("must not be called")})
		if _, err := store.Save(ctx, exec, 0); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("Save = %v, want ErrExecutionConflict", err)
		}
	})
}

func TestMisconfiguredStoreReturnsErrors(t *testing.T) {
	var db DB
	for name, store := range map[string]*Store{
		"nil database":      New(db),
		"invalid table":     New(&fakeDB{}, WithTableName("bad\x00name")),
		"nil store pointer": nil,
	} {
		if _, err := store.Load(context.Background(), "x"); err == nil {
			t.Errorf("%s: Load returned nil error", name)
		}
	}
}

func TestWithTableNameIgnoresEmptyAndSanitizes(t *testing.T) {
	if store := New(&fakeDB{}, WithTableName("")); store.table != `"executions"` {
		t.Errorf("table = %s, want the sanitized default", store.table)
	}
	if store := New(&fakeDB{}, WithTableName(`we"ird`)); store.table != `"we""ird"` {
		t.Errorf("table = %s, want a sanitized identifier", store.table)
	}
}
