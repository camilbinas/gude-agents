package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/conformance"
)

func newFileStore(t *testing.T) agent.ExecutionStore {
	t.Helper()
	s, err := New("file:" + filepath.Join(t.TempDir(), "exec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConformance(t *testing.T) { conformance.Run(t, newFileStore) }

func TestAgentProtocol(t *testing.T) { conformance.RunAgent(t, newFileStore) }

func TestMemoryDSNConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) agent.ExecutionStore {
		s, err := New(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// A paused execution must still be there after the store is closed and the
// file is opened again, as after a process restart.
func TestSurvivesReopen(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "exec.db")
	ctx := context.Background()
	first, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.Create(ctx, conformance.Full("reopen"))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	loaded, err := second.Load(ctx, "reopen")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != created.Version || loaded.Status != agent.ExecutionPaused {
		t.Fatalf("loaded = version %d status %q", loaded.Version, loaded.Status)
	}
}

// Many writers race on one version through separate stores on one file; only
// one may win.
func TestConcurrentSaveHasOneWinner(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "exec.db")
	ctx := context.Background()
	seed, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	created, err := seed.Create(ctx, conformance.Full("race"))
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := New(dsn)
			if err != nil {
				results <- err
				return
			}
			defer s.Close()
			_, err = s.Save(ctx, created, created.Version)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, agent.ErrExecutionConflict):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d writers won, want exactly 1", wins)
	}
}

func TestPayloadIsQueryableJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec.db")
	s, err := New("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Create(context.Background(), conformance.Full("sql-query")); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT json_extract(payload, '$.status') FROM executions WHERE execution_id = ?`, "sql-query").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(agent.ExecutionPaused) {
		t.Fatalf("status = %q", status)
	}
}

func TestConfigurationErrors(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("empty dsn must fail")
	}
	if _, err := New(":memory:", WithTableName("bad\x00name")); err == nil {
		t.Fatal("NUL in table name must fail")
	}
	var nilStore *Store
	if _, err := nilStore.Load(context.Background(), "x"); err == nil {
		t.Fatal("nil store must fail")
	}
}

func TestCustomTableName(t *testing.T) {
	s, err := New(":memory:", WithTableName(`odd "name"`))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Create(context.Background(), conformance.Full("t")); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s`, identifier(`odd "name"`))).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, err = %v", n, err)
	}
}
