package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/quick"

	agent "github.com/camilbinas/gude-agents/agent"
)

func TestPropertyConcurrentTPMAdmissionNeverOversubscribes(t *testing.T) {
	property := func(limit, estimate uint8) bool {
		if limit < 1 || estimate < 1 || estimate > limit {
			return true
		}
		limiter := limiterForTest(t, TPM(int(limit)), WithTokenEstimator(fixedEstimator{n: int(estimate)}), WithFailFast())
		var admitted atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				lease, err := limiter.Reserve(context.Background(), agent.RateLimitRequest{Key: "key"})
				if err == nil {
					admitted.Add(1)
					_ = limiter.Release(context.Background(), lease)
				}
			}()
		}
		close(start)
		wg.Wait()
		return int(admitted.Load()) <= int(limit)/int(estimate)
	}
	if err := quick.Check(property, nil); err != nil {
		t.Fatal(err)
	}
}
