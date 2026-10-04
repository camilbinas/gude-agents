package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	agent "github.com/camilbinas/gude-agents/agent"
)

// Lease owns one provider attempt's admission state.
type Lease struct {
	limiter *RateLimiter
	id      string
	key     string

	estimate int
	tokens   []TokenReservation
	buckets  []*rateBucket
	store    RateLimitLeaseStore
	release  ReleaseFunc

	mu       sync.Mutex
	terminal bool
}

// AcquireLease reserves rate capacity for one provider attempt.
func (rl *RateLimiter) AcquireLease(ctx context.Context, key string, req ModelRequest) (agent.RateLimitLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	estimate := 0
	if rl.tokenEstimator != nil && !rl.preFlightDisabled && (rl.tokenRateLimit != nil || rl.globalTokenLimit != nil) {
		if n, err := rl.tokenEstimator.EstimateTokens(ctx, req); err == nil && n > 0 {
			estimate = n
		}
	}
	id, err := newRateLimitLeaseID()
	if err != nil {
		return nil, err
	}

	release := ReleaseFunc(noopRelease)
	if rl.maxConcurrent > 0 {
		sem := rl.leaseSem(key)
		if err := sem.Acquire(ctx, rl.overflowBehavior); err != nil {
			rl.unleaseSem(sem)
			return nil, err
		}
		var once sync.Once
		release = func() {
			once.Do(func() {
				sem.Release()
				rl.unleaseSem(sem)
			})
		}
	}

	lease := &Lease{limiter: rl, id: id, key: key, estimate: estimate, tokens: rl.tokenReservations(key, estimate), release: release}
	if rl.store != nil {
		store, ok := rl.store.(RateLimitLeaseStore)
		if !ok {
			release()
			return nil, errors.New("rate limit store does not support lease reservations")
		}
		lease.store = store
		if err := rl.reserveLeaseWithStore(ctx, store, RateLimitReservation{ID: id, Requests: rl.requestReservations(key), Tokens: lease.tokens}); err != nil {
			release()
			return nil, err
		}
		return lease, nil
	}

	buckets := rl.limitBuckets(key)
	if err := reserveLeaseBuckets(ctx, rl.overflowBehavior, buckets, estimate); err != nil {
		release()
		return nil, err
	}
	lease.buckets = buckets
	return lease, nil
}

// Commit reconciles estimated and actual token usage.
func (l *Lease) Commit(ctx context.Context, usage TokenUsage) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal {
		return nil
	}
	actual := usage.Total()
	if actual < 0 {
		actual = 0
	}
	if l.store != nil {
		accCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
		defer cancel()
		if err := l.store.CommitLease(accCtx, l.id, l.tokens, actual); err != nil {
			return err
		}
	} else {
		commitLeaseBuckets(l.buckets, l.estimate, actual)
	}
	l.terminal = true
	return nil
}

// Fail conservatively consumes the estimated token reservation.
func (l *Lease) Fail(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal {
		return nil
	}
	if l.store != nil {
		accCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
		defer cancel()
		if err := l.store.FailLease(accCtx, l.id, l.tokens); err != nil {
			return err
		}
	} else {
		failLeaseBuckets(l.buckets, l.estimate)
	}
	l.terminal = true
	return nil
}

// Release frees the process-local concurrency slot.
func (l *Lease) Release() { l.release() }

func newRateLimitLeaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate rate limit lease ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (rl *RateLimiter) tokenReservations(key string, estimate int) []TokenReservation {
	var out []TokenReservation
	if rl.tokenRateLimit != nil {
		out = append(out, TokenReservation{Key: storeKey(key), Limit: rl.tokenRateLimit.Count, Window: rl.tokenRateLimit.window(), Strategy: rl.windowStrategy, Amount: estimate})
	}
	if rl.globalTokenLimit != nil {
		out = append(out, TokenReservation{Key: storeGlobalKey, Limit: rl.globalTokenLimit.Count, Window: rl.globalTokenLimit.window(), Strategy: rl.windowStrategy, Amount: estimate})
	}
	return out
}

func (rl *RateLimiter) reserveLeaseWithStore(ctx context.Context, store RateLimitLeaseStore, reservation RateLimitReservation) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := store.ReserveLease(ctx, reservation)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if rl.overflowBehavior == FailFastMode {
			return ErrRateLimitExceeded
		}
		if err := waitFor(ctx, storePollInterval); err != nil {
			return err
		}
	}
}

func reserveLeaseBuckets(ctx context.Context, behavior OverflowBehavior, buckets []*rateBucket, estimate int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lockBuckets(buckets)
		admit := true
		var wait time.Duration
		for _, b := range buckets {
			if b.rpmLimit > 0 {
				count := b.slidingRPMCount()
				if b.windowStrategy == FixedWindow {
					count = b.fixedRPMCountVal()
				}
				if count >= b.rpmLimit {
					admit = false
					if w := b.rpmWaitDuration(); w > wait {
						wait = w
					}
				}
			}
			if b.tpmLimit > 0 && b.tokenCountLocked()+b.reservedTPM+estimate > b.tpmLimit {
				admit = false
				if w := b.tpmWaitDuration(); w > wait {
					wait = w
				}
			}
		}
		if admit {
			for _, b := range buckets {
				b.chargeRequestLocked()
				if b.tpmLimit > 0 {
					b.reservedTPM += estimate
				}
			}
			unlockBuckets(buckets)
			return nil
		}
		unlockBuckets(buckets)
		if behavior == FailFastMode {
			return ErrRateLimitExceeded
		}
		if wait < 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		if err := waitFor(ctx, wait); err != nil {
			return err
		}
	}
}

func commitLeaseBuckets(buckets []*rateBucket, estimate, actual int) {
	lockBuckets(buckets)
	for _, b := range buckets {
		if b.tpmLimit > 0 {
			b.reservedTPM -= estimate
			if b.reservedTPM < 0 {
				b.reservedTPM = 0
			}
			b.recordLocked(actual)
		}
	}
	unlockBuckets(buckets)
}

func failLeaseBuckets(buckets []*rateBucket, estimate int) {
	commitLeaseBuckets(buckets, estimate, estimate)
}
