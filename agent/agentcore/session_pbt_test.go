package agentcore

import (
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 5: Per-session sequential processing

// TestProperty5_PerSessionSequentialProcessing verifies that for any set of events
// sharing the same session ID submitted concurrently to the runtime, the processing
// of those events is serialized — no two events for the same session ID have
// overlapping processing windows.
//
// **Validates: Requirements 3.7**
func TestProperty5_PerSessionSequentialProcessing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random session ID.
		sessionID := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "sessionID")
		// Generate N concurrent goroutines (2-10) per task spec.
		numGoroutines := rapid.IntRange(2, 10).Draw(t, "numGoroutines")
		// Generate a sleep duration between 1-5ms to simulate processing work.
		sleepMs := rapid.IntRange(1, 5).Draw(t, "sleepMs")
		sleepDuration := time.Duration(sleepMs) * time.Millisecond

		// Create a Runtime — we only need the sessionMutex mechanism.
		rt := &Runtime{
			sessions: make(map[string]*sync.Mutex),
		}

		// Track processing windows: each goroutine records its start and end time.
		type window struct {
			start time.Time
			end   time.Time
		}
		windows := make([]window, numGoroutines)
		var windowsMu sync.Mutex
		var wg sync.WaitGroup

		// Dispatch all goroutines concurrently, each acquiring the per-session mutex.
		wg.Add(numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func(idx int) {
				defer wg.Done()

				// Acquire the per-session mutex (same mechanism as pollLoop).
				sessMu := rt.sessionMutex(sessionID)
				sessMu.Lock()
				defer sessMu.Unlock()

				// Record processing window with simulated work.
				start := time.Now()
				time.Sleep(sleepDuration)
				end := time.Now()

				windowsMu.Lock()
				windows[idx] = window{start: start, end: end}
				windowsMu.Unlock()
			}(i)
		}

		wg.Wait()

		// Verify no two goroutines have overlapping [start, end] windows.
		// For sequential processing, for any pair (i, j), either
		// windows[i].end <= windows[j].start OR windows[j].end <= windows[i].start.
		for i := 0; i < numGoroutines; i++ {
			for j := i + 1; j < numGoroutines; j++ {
				wi := windows[i]
				wj := windows[j]
				iBeforeJ := !wi.end.After(wj.start)
				jBeforeI := !wj.end.After(wi.start)
				if !iBeforeJ && !jBeforeI {
					t.Fatalf("overlapping processing windows detected for session %q: "+
						"goroutine %d [%v, %v] overlaps with goroutine %d [%v, %v]",
						sessionID, i, wi.start, wi.end, j, wj.start, wj.end)
				}
			}
		}
	})
}
