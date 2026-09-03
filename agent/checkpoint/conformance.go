package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// RunConformance exercises the behaviour every Checkpointer implementation must
// share. Backend packages call it from their own tests against a live store:
//
//	func TestConformance(t *testing.T) {
//	    checkpoint.RunConformance(t, func(t *testing.T) checkpoint.Checkpointer {
//	        return newTestStore(t)
//	    })
//	}
//
// newStore must return an empty store; each subtest uses distinct thread IDs so a
// shared backing store is acceptable.
func RunConformance(t *testing.T, newStore func(t *testing.T) Checkpointer) {
	t.Helper()
	ctx := context.Background()

	t.Run("SaveAssignsSequentialVersionsFromOne", func(t *testing.T) {
		c := newStore(t)
		for want := 1; want <= 3; want++ {
			got, err := c.Save(ctx, "conf-seq", Checkpoint{State: State{"n": want}})
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			if got.Version != want {
				t.Errorf("version = %d, want %d", got.Version, want)
			}
			if got.ThreadID != "conf-seq" {
				t.Errorf("threadID = %q, want conf-seq", got.ThreadID)
			}
		}
	})

	t.Run("SaveDefaultsTimestamp", func(t *testing.T) {
		c := newStore(t)
		got, err := c.Save(ctx, "conf-ts", Checkpoint{})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		if got.Timestamp.IsZero() {
			t.Error("timestamp was not defaulted")
		}
	})

	t.Run("LoadReturnsLatest", func(t *testing.T) {
		c := newStore(t)
		for i := 1; i <= 3; i++ {
			if _, err := c.Save(ctx, "conf-latest", Checkpoint{State: State{"n": i}}); err != nil {
				t.Fatalf("save: %v", err)
			}
		}
		got, err := c.Load(ctx, "conf-latest")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.Version != 3 {
			t.Errorf("version = %d, want 3", got.Version)
		}
	})

	t.Run("LoadMissingThreadReturnsErrNotFound", func(t *testing.T) {
		c := newStore(t)
		if _, err := c.Load(ctx, "conf-absent"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("LoadAtExactVersion", func(t *testing.T) {
		c := newStore(t)
		for i := 1; i <= 3; i++ {
			if _, err := c.Save(ctx, "conf-at", Checkpoint{Label: "n"}); err != nil {
				t.Fatalf("save: %v", err)
			}
		}
		got, err := c.LoadAt(ctx, "conf-at", 2)
		if err != nil {
			t.Fatalf("loadAt: %v", err)
		}
		if got.Version != 2 {
			t.Errorf("version = %d, want 2", got.Version)
		}
		if _, err := c.LoadAt(ctx, "conf-at", 99); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	// Extra is opaque to backends. Its JSON meaning, including false-valued map
	// entries, must survive even when a backend normalizes the encoding.
	t.Run("ExtraRoundTripsWithoutFieldLoss", func(t *testing.T) {
		c := newStore(t)
		type payload struct {
			Completed map[string]bool `json:"completed"`
			Readiness map[string]bool `json:"readiness"`
			Counter   int             `json:"counter"`
			Nested    struct {
				Deep string `json:"deep"`
			} `json:"nested"`
		}
		in := payload{
			Completed: map[string]bool{"a": true, "b": false},
			Readiness: map[string]bool{"out_a": true, "out_b": false},
			Counter:   42,
		}
		in.Nested.Deep = "value"

		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := c.Save(ctx, "conf-extra", Checkpoint{Extra: raw}); err != nil {
			t.Fatalf("save: %v", err)
		}
		loaded, err := c.Load(ctx, "conf-extra")
		if err != nil {
			t.Fatalf("load: %v", err)
		}

		var out payload
		if err := json.Unmarshal(loaded.Extra, &out); err != nil {
			t.Fatalf("unmarshal extra: %v (raw=%s)", err, loaded.Extra)
		}
		if out.Counter != 42 {
			t.Errorf("counter = %d, want 42", out.Counter)
		}
		if out.Nested.Deep != "value" {
			t.Errorf("nested.deep = %q, want value", out.Nested.Deep)
		}
		for _, tc := range []struct {
			field string
			m     map[string]bool
			key   string
			want  bool
		}{
			{"completed", out.Completed, "a", true},
			{"completed", out.Completed, "b", false},
			{"readiness", out.Readiness, "out_a", true},
			{"readiness", out.Readiness, "out_b", false},
		} {
			got, ok := tc.m[tc.key]
			if !ok {
				t.Errorf("%s[%s] missing — a %v-valued entry was dropped", tc.field, tc.key, tc.want)
				continue
			}
			if got != tc.want {
				t.Errorf("%s[%s] = %v, want %v", tc.field, tc.key, got, tc.want)
			}
		}
	})

	t.Run("StateRoundTrips", func(t *testing.T) {
		c := newStore(t)
		if _, err := c.Save(ctx, "conf-state", Checkpoint{
			State: State{"str": "s", "num": float64(3), "flag": true},
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := c.Load(ctx, "conf-state")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.State["str"] != "s" {
			t.Errorf("state[str] = %v, want s", got.State["str"])
		}
		if got.State["num"] != float64(3) {
			t.Errorf("state[num] = %v, want 3", got.State["num"])
		}
		if got.State["flag"] != true {
			t.Errorf("state[flag] = %v, want true", got.State["flag"])
		}
	})

	t.Run("HistoryOldestFirstWithLabels", func(t *testing.T) {
		c := newStore(t)
		for _, l := range []string{"one", "two", "three"} {
			if _, err := c.Save(ctx, "conf-hist", Checkpoint{Label: l}); err != nil {
				t.Fatalf("save: %v", err)
			}
		}
		metas, err := c.History(ctx, "conf-hist")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(metas) != 3 {
			t.Fatalf("len = %d, want 3", len(metas))
		}
		for i, want := range []string{"one", "two", "three"} {
			if metas[i].Version != i+1 {
				t.Errorf("metas[%d].Version = %d, want %d", i, metas[i].Version, i+1)
			}
			if metas[i].Label != want {
				t.Errorf("metas[%d].Label = %q, want %q", i, metas[i].Label, want)
			}
		}
	})

	t.Run("HistoryOfUnknownThreadIsEmptyNotError", func(t *testing.T) {
		c := newStore(t)
		metas, err := c.History(ctx, "conf-hist-absent")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(metas) != 0 {
			t.Errorf("len = %d, want 0", len(metas))
		}
	})

	t.Run("ListIncludesSavedThreads", func(t *testing.T) {
		c := newStore(t)
		if _, err := c.Save(ctx, "conf-list-a", Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if _, err := c.Save(ctx, "conf-list-b", Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
		ids, err := c.List(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		found := map[string]bool{}
		for _, id := range ids {
			found[id] = true
		}
		for _, want := range []string{"conf-list-a", "conf-list-b"} {
			if !found[want] {
				t.Errorf("List() missing %q (got %v)", want, ids)
			}
		}
	})

	t.Run("DeleteRemovesThread", func(t *testing.T) {
		c := newStore(t)
		if _, err := c.Save(ctx, "conf-del", Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := c.Delete(ctx, "conf-del"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := c.Load(ctx, "conf-del"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteUnknownThreadIsNotError", func(t *testing.T) {
		c := newStore(t)
		if err := c.Delete(ctx, "conf-del-absent"); err != nil {
			t.Errorf("delete: %v", err)
		}
	})

	t.Run("ConcurrentSavesAssignUniqueVersions", func(t *testing.T) {
		c := newStore(t)
		const count = 16

		versions := make(chan int, count)
		errs := make(chan error, count)
		var wg sync.WaitGroup
		wg.Add(count)
		for range count {
			go func() {
				defer wg.Done()
				got, err := c.Save(ctx, "conf-concurrent", Checkpoint{})
				if err != nil {
					errs <- err
					return
				}
				versions <- got.Version
			}()
		}
		wg.Wait()
		close(errs)
		close(versions)

		for err := range errs {
			t.Errorf("save: %v", err)
		}
		seen := make(map[int]bool, count)
		for version := range versions {
			if seen[version] {
				t.Errorf("duplicate version %d", version)
			}
			seen[version] = true
		}
		if len(seen) != count {
			t.Fatalf("saved versions = %d, want %d", len(seen), count)
		}
		for version := 1; version <= count; version++ {
			if !seen[version] {
				t.Errorf("missing version %d", version)
			}
		}
	})

	t.Run("ThreadsHaveIndependentVersionSequences", func(t *testing.T) {
		c := newStore(t)
		if _, err := c.Save(ctx, "conf-iso-a", Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if _, err := c.Save(ctx, "conf-iso-a", Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := c.Save(ctx, "conf-iso-b", Checkpoint{})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		if got.Version != 1 {
			t.Errorf("second thread first version = %d, want 1", got.Version)
		}
	})
}
