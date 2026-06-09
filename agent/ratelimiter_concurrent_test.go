package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentAcquire launches N goroutines each calling Acquire M times on a
// shared RateLimiter with high limits. Verifies total successful acquires equals N*M.
// Uses a frozen mock clock and WithFailFast() to avoid blocking.
//
// Validates: Requirements 4.4, 5.4, 8.1, 8.2, 8.3
func TestConcurrentAcquire(t *testing.T) {
	const numGoroutines = 50
	const acquiresPerGoroutine = 100

	// High RPM limit so all acquires succeed
	rl, err := NewRateLimiter(RPM(numGoroutines*acquiresPerGoroutine+1), WithSlidingWindow(), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	// Freeze time so nothing expires during the test
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return fixedTime }

	ctx := context.Background()
	var wg sync.WaitGroup
	var successCount atomic.Int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < acquiresPerGoroutine; j++ {
				if _, err := rl.Acquire(ctx, ""); err != nil {
					t.Errorf("Acquire() returned unexpected error: %v", err)
					return
				}
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	expected := int64(numGoroutines * acquiresPerGoroutine)
	got := successCount.Load()
	if got != expected {
		t.Fatalf("expected %d successful acquires, got %d", expected, got)
	}

	// Verify the RPM counter matches
	b := rl.bucket("")
	b.mu.Lock()
	rpmCount := b.slidingRPMCount()
	b.mu.Unlock()

	if rpmCount != int(expected) {
		t.Fatalf("RPM counter = %d, want %d", rpmCount, expected)
	}
}

// TestConcurrentRecord launches N goroutines each calling Record with known token
// amounts. Verifies the total TPM counter equals the sum of all recorded tokens.
//
// Validates: Requirements 4.4, 5.4, 8.1, 8.2, 8.3
func TestConcurrentRecord(t *testing.T) {
	const numGoroutines = 50
	const recordsPerGoroutine = 100
	const tokensPerRecord = 10 // 5 input + 5 output

	// TPM high enough to not trigger limits
	rl, err := NewRateLimiter(RPM(1), TPM(numGoroutines*recordsPerGoroutine*tokensPerRecord+1), WithSlidingWindow(), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	// Freeze time so nothing expires during the test
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return fixedTime }

	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				rl.Record("", TokenUsage{
					InputTokens:  5,
					OutputTokens: 5,
				})
			}
		}()
	}

	wg.Wait()

	// Verify the TPM counter equals the sum of all recorded tokens
	b := rl.bucket("")
	b.mu.Lock()
	tpmCount := b.slidingTPMCount()
	b.mu.Unlock()

	expected := numGoroutines * recordsPerGoroutine * tokensPerRecord
	if tpmCount != expected {
		t.Fatalf("TPM counter = %d, want %d", tpmCount, expected)
	}
}

// TestConcurrentAcquireAndRecord launches goroutines doing both Acquire and Record
// concurrently on a shared RateLimiter. Verifies no panics or data races occur.
//
// Validates: Requirements 4.4, 5.4, 8.1, 8.2, 8.3
func TestConcurrentAcquireAndRecord(t *testing.T) {
	const numGoroutines = 50
	const opsPerGoroutine = 100

	// High limits so nothing gets rate-limited
	rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithSlidingWindow(), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	// Freeze time so nothing expires during the test
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return fixedTime }

	ctx := context.Background()
	var wg sync.WaitGroup
	var acquireCount atomic.Int64
	var recordCount atomic.Int64

	// Half the goroutines do Acquire, half do Record
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		if i%2 == 0 {
			go func() {
				defer wg.Done()
				for j := 0; j < opsPerGoroutine; j++ {
					if _, err := rl.Acquire(ctx, ""); err != nil {
						t.Errorf("Acquire() returned unexpected error: %v", err)
						return
					}
					acquireCount.Add(1)
				}
			}()
		} else {
			go func() {
				defer wg.Done()
				for j := 0; j < opsPerGoroutine; j++ {
					rl.Record("", TokenUsage{
						InputTokens:  3,
						OutputTokens: 7,
					})
					recordCount.Add(1)
				}
			}()
		}
	}

	wg.Wait()

	// Verify counts are as expected (no lost operations)
	expectedAcquires := int64((numGoroutines / 2) * opsPerGoroutine)
	expectedRecords := int64((numGoroutines / 2) * opsPerGoroutine)

	if acquireCount.Load() != expectedAcquires {
		t.Fatalf("acquire count = %d, want %d", acquireCount.Load(), expectedAcquires)
	}
	if recordCount.Load() != expectedRecords {
		t.Fatalf("record count = %d, want %d", recordCount.Load(), expectedRecords)
	}

	// Verify internal counters are consistent
	b := rl.bucket("")
	b.mu.Lock()
	rpmCount := b.slidingRPMCount()
	tpmCount := b.slidingTPMCount()
	b.mu.Unlock()

	if rpmCount != int(expectedAcquires) {
		t.Fatalf("RPM counter = %d, want %d", rpmCount, expectedAcquires)
	}

	expectedTPM := int(expectedRecords) * 10 // 3 + 7 = 10 tokens per record
	if tpmCount != expectedTPM {
		t.Fatalf("TPM counter = %d, want %d", tpmCount, expectedTPM)
	}
}

// TestSharedLimiterAcrossAgents simulates multiple "agents" (goroutines) sharing
// a single RateLimiter, each doing Acquire+Record. Verifies the aggregated counters
// reflect all agents' consumption.
//
// Validates: Requirements 4.4, 5.4, 8.1, 8.2, 8.3
func TestSharedLimiterAcrossAgents(t *testing.T) {
	const numAgents = 20
	const callsPerAgent = 50
	const tokensPerCall = 100 // 40 input + 60 output

	// High limits so all operations succeed
	totalExpectedRPM := numAgents * callsPerAgent
	totalExpectedTPM := numAgents * callsPerAgent * tokensPerCall

	rl, err := NewRateLimiter(RPM(totalExpectedRPM+1), TPM(totalExpectedTPM+1), WithSlidingWindow(), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	// Freeze time so nothing expires during the test
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return fixedTime }

	ctx := context.Background()
	var wg sync.WaitGroup
	var totalAcquires atomic.Int64
	var totalTokensRecorded atomic.Int64

	// Each "agent" goroutine does Acquire followed by Record (simulating a provider call)
	for agent := 0; agent < numAgents; agent++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for call := 0; call < callsPerAgent; call++ {
				// Acquire (pre-call)
				if _, err := rl.Acquire(ctx, ""); err != nil {
					t.Errorf("Agent Acquire() returned unexpected error: %v", err)
					return
				}
				totalAcquires.Add(1)

				// Record (post-call) — simulating a successful provider response
				rl.Record("", TokenUsage{
					InputTokens:  40,
					OutputTokens: 60,
				})
				totalTokensRecorded.Add(tokensPerCall)
			}
		}()
	}

	wg.Wait()

	// Verify all agents completed their operations
	if totalAcquires.Load() != int64(totalExpectedRPM) {
		t.Fatalf("total acquires = %d, want %d", totalAcquires.Load(), totalExpectedRPM)
	}
	if totalTokensRecorded.Load() != int64(totalExpectedTPM) {
		t.Fatalf("total tokens recorded = %d, want %d", totalTokensRecorded.Load(), totalExpectedTPM)
	}

	// Verify the shared RateLimiter's internal counters reflect aggregated consumption
	b := rl.bucket("")
	b.mu.Lock()
	rpmCount := b.slidingRPMCount()
	tpmCount := b.slidingTPMCount()
	b.mu.Unlock()

	if rpmCount != totalExpectedRPM {
		t.Fatalf("aggregated RPM counter = %d, want %d", rpmCount, totalExpectedRPM)
	}
	if tpmCount != totalExpectedTPM {
		t.Fatalf("aggregated TPM counter = %d, want %d", tpmCount, totalExpectedTPM)
	}
}

// TestConcurrentAcquire_FixedWindow repeats the concurrent acquire test with
// FixedWindow strategy to ensure both strategies are concurrency-safe.
//
// Validates: Requirements 4.4, 8.1, 8.2, 8.3
func TestConcurrentAcquire_FixedWindow(t *testing.T) {
	const numGoroutines = 50
	const acquiresPerGoroutine = 100

	rl, err := NewRateLimiter(RPM(numGoroutines*acquiresPerGoroutine+1), WithFixedWindow(), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	// Freeze time so the fixed window never resets
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return fixedTime }

	ctx := context.Background()
	var wg sync.WaitGroup
	var successCount atomic.Int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < acquiresPerGoroutine; j++ {
				if _, err := rl.Acquire(ctx, ""); err != nil {
					t.Errorf("Acquire() returned unexpected error: %v", err)
					return
				}
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	expected := int64(numGoroutines * acquiresPerGoroutine)
	got := successCount.Load()
	if got != expected {
		t.Fatalf("expected %d successful acquires, got %d", expected, got)
	}

	// Verify the fixed window RPM counter matches
	b := rl.bucket("")
	b.mu.Lock()
	rpmCount := b.fixedRPMCount
	b.mu.Unlock()

	if rpmCount != int(expected) {
		t.Fatalf("fixed window RPM counter = %d, want %d", rpmCount, expected)
	}
}

// TestConcurrentRecord_FixedWindow repeats the concurrent record test with
// FixedWindow strategy to ensure both strategies are concurrency-safe.
//
// Validates: Requirements 5.4, 8.1, 8.2, 8.3
func TestConcurrentRecord_FixedWindow(t *testing.T) {
	const numGoroutines = 50
	const recordsPerGoroutine = 100
	const tokensPerRecord = 10

	rl, err := NewRateLimiter(RPM(1), TPM(numGoroutines*recordsPerGoroutine*tokensPerRecord+1), WithFixedWindow(), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	// Freeze time so the fixed window never resets
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return fixedTime }

	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				rl.Record("", TokenUsage{
					InputTokens:  5,
					OutputTokens: 5,
				})
			}
		}()
	}

	wg.Wait()

	// Verify the fixed window TPM counter
	b := rl.bucket("")
	b.mu.Lock()
	tpmCount := b.fixedTPMCount
	b.mu.Unlock()

	expected := numGoroutines * recordsPerGoroutine * tokensPerRecord
	if tpmCount != expected {
		t.Fatalf("fixed window TPM counter = %d, want %d", tpmCount, expected)
	}
}
