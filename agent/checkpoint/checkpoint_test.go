package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

func TestSave_AssignsSequentialVersions(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()

	for want := 1; want <= 3; want++ {
		got, err := c.Save(ctx, "t1", Checkpoint{State: State{"n": want}})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		if got.Version != want {
			t.Errorf("version = %d, want %d", got.Version, want)
		}
		if got.ThreadID != "t1" {
			t.Errorf("threadID = %q, want t1", got.ThreadID)
		}
	}
}

func TestSave_RejectsEmptyThreadID(t *testing.T) {
	c := NewMemory()
	if _, err := c.Save(context.Background(), "", Checkpoint{}); !errors.Is(err, ErrThreadIDRequired) {
		t.Errorf("err = %v, want ErrThreadIDRequired", err)
	}
}

func TestSave_DefaultsTimestamp(t *testing.T) {
	c := NewMemory()
	got, err := c.Save(context.Background(), "t1", Checkpoint{})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got.Timestamp.IsZero() {
		t.Error("expected timestamp to be defaulted, got zero")
	}
}

func TestSave_PreservesExplicitTimestamp(t *testing.T) {
	c := NewMemory()
	ts := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	got, err := c.Save(context.Background(), "t1", Checkpoint{Timestamp: ts})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("timestamp = %v, want %v", got.Timestamp, ts)
	}
}

// Backends must preserve the meaning of opaque consumer JSON, including map
// entries whose value is false. This guards against payload decomposition losing
// fields unknown to a backend.
func TestExtra_RoundTripsWithoutFieldLossIncludingFalseValues(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()

	type scheduler struct {
		Completed  map[string]bool `json:"completed"`
		Readiness  map[string]bool `json:"readiness"`
		Iterations int             `json:"iterations"`
	}
	in := scheduler{
		Completed:  map[string]bool{"a": true},
		Readiness:  map[string]bool{"output_a": true, "output_b": false},
		Iterations: 7,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := c.Save(ctx, "t1", Checkpoint{Extra: raw}); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := c.Load(ctx, "t1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var out scheduler
	if err := json.Unmarshal(loaded.Extra, &out); err != nil {
		t.Fatalf("unmarshal extra: %v", err)
	}
	if out.Iterations != 7 {
		t.Errorf("iterations = %d, want 7", out.Iterations)
	}
	if !out.Readiness["output_a"] {
		t.Error("readiness[output_a] = false, want true")
	}
	if _, ok := out.Readiness["output_b"]; !ok {
		t.Error("readiness[output_b] missing — a false-valued entry was dropped")
	}
	if out.Readiness["output_b"] {
		t.Error("readiness[output_b] = true, want false")
	}
}

func TestLoad_NotFound(t *testing.T) {
	c := NewMemory()
	if _, err := c.Load(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLoad_ReturnsLatestVersion(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		if _, err := c.Save(ctx, "t1", Checkpoint{State: State{"n": i}}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	got, err := c.Load(ctx, "t1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Version != 3 {
		t.Errorf("version = %d, want 3", got.Version)
	}
}

func TestLoadAt_ExactVersionAndNotFound(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		if _, err := c.Save(ctx, "t1", Checkpoint{State: State{"n": i}}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	got, err := c.LoadAt(ctx, "t1", 2)
	if err != nil {
		t.Fatalf("loadAt: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
	if got.State["n"] != 2 {
		t.Errorf("state[n] = %v, want 2", got.State["n"])
	}

	if _, err := c.LoadAt(ctx, "t1", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestHistory_OldestFirstWithLabels(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()
	for _, label := range []string{"first", "second", "third"} {
		if _, err := c.Save(ctx, "t1", Checkpoint{Label: label}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	metas, err := c.History(ctx, "t1")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("len = %d, want 3", len(metas))
	}
	for i, want := range []string{"first", "second", "third"} {
		if metas[i].Label != want {
			t.Errorf("metas[%d].Label = %q, want %q", i, metas[i].Label, want)
		}
		if metas[i].Version != i+1 {
			t.Errorf("metas[%d].Version = %d, want %d", i, metas[i].Version, i+1)
		}
	}
}

func TestHistory_EmptyThreadIsNotAnError(t *testing.T) {
	c := NewMemory()
	metas, err := c.History(context.Background(), "nope")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("len = %d, want 0", len(metas))
	}
}

func TestListAndDelete(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := c.Save(ctx, id, Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	ids, err := c.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("len = %d, want 2", len(ids))
	}

	if err := c.Delete(ctx, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.Load(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}

	// Deleting a nonexistent thread is not an error.
	if err := c.Delete(ctx, "ghost"); err != nil {
		t.Errorf("delete nonexistent: %v", err)
	}
}

func TestSave_IsolatesStateFromCallerMutation(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()

	st := State{"k": "original"}
	if _, err := c.Save(ctx, "t1", Checkpoint{State: st}); err != nil {
		t.Fatalf("save: %v", err)
	}
	st["k"] = "mutated"

	loaded, err := c.Load(ctx, "t1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.State["k"] != "original" {
		t.Errorf("state[k] = %v, want original — caller mutation leaked into store", loaded.State["k"])
	}
}

func TestSave_IsolatesExtraFromCallerMutation(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()

	raw := []byte(`{"v":1}`)
	if _, err := c.Save(ctx, "t1", Checkpoint{Extra: raw}); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw[5] = '9'

	loaded, err := c.Load(ctx, "t1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(loaded.Extra) != `{"v":1}` {
		t.Errorf("extra = %s, want {\"v\":1}", loaded.Extra)
	}
}

func TestUsageIsPersisted(t *testing.T) {
	c := NewMemory()
	want := agent.TokenUsage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 2}
	if _, err := c.Save(context.Background(), "t1", Checkpoint{Usage: want}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := c.Load(context.Background(), "t1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Usage != want {
		t.Errorf("usage = %+v, want %+v", got.Usage, want)
	}
}

func TestThreadsAreIsolated(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()
	if _, err := c.Save(ctx, "a", Checkpoint{State: State{"who": "a"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := c.Save(ctx, "b", Checkpoint{State: State{"who": "b"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Independent version sequences.
	got, err := c.Load(ctx, "b")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("thread b version = %d, want 1", got.Version)
	}
	if got.State["who"] != "b" {
		t.Errorf("state[who] = %v, want b", got.State["who"])
	}
}

func TestConcurrentSaveIsSafe(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()

	const n = 50
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := c.Save(ctx, "shared", Checkpoint{}); err != nil {
				t.Errorf("save: %v", err)
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	metas, err := c.History(ctx, "shared")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(metas) != n {
		t.Fatalf("len = %d, want %d", len(metas), n)
	}
	// Versions must be a contiguous 1..n sequence with no duplicates.
	seen := make(map[int]bool, n)
	for _, m := range metas {
		if seen[m.Version] {
			t.Fatalf("duplicate version %d", m.Version)
		}
		seen[m.Version] = true
	}
	for v := 1; v <= n; v++ {
		if !seen[v] {
			t.Errorf("missing version %d", v)
		}
	}
}
