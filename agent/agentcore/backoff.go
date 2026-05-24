package agentcore

import (
	"math"
	"math/rand"
	"time"
)

// backoffConfig holds retry/backoff parameters for AgentCore API calls.
type backoffConfig struct {
	baseDelay  time.Duration
	maxDelay   time.Duration
	maxRetries int     // 0 = infinite retries
	jitterPct  float64 // 0.25 = ±25% jitter
}

// delay computes the backoff delay for the given attempt number (0-indexed).
// It calculates min(baseDelay * 2^attempt, maxDelay) and then applies random
// jitter of ±jitterPct around that value.
func (b backoffConfig) delay(attempt int) time.Duration {
	// Compute base delay with exponential growth, guarding against overflow.
	base := float64(b.baseDelay)
	exp := math.Pow(2, float64(attempt))
	delayed := base * exp

	// Cap at maxDelay. Also handles overflow (Inf or very large values).
	maxD := float64(b.maxDelay)
	if delayed > maxD || math.IsInf(delayed, 1) || math.IsNaN(delayed) {
		delayed = maxD
	}

	// Apply jitter: random value in [-jitterPct, +jitterPct] of the delay.
	jitter := (rand.Float64()*2 - 1) * b.jitterPct * delayed
	result := delayed + jitter

	// Ensure we never return a negative duration.
	if result < 0 {
		result = 0
	}

	return time.Duration(result)
}

// Default backoff configurations for different AgentCore operations.
var (
	// heartbeatBackoff is used for heartbeat retries: 3 retries, 1s–10s range.
	heartbeatBackoff = backoffConfig{
		baseDelay:  1 * time.Second,
		maxDelay:   10 * time.Second,
		maxRetries: 3,
		jitterPct:  0.25,
	}

	// pollBackoff is used for event poll retries: infinite retries, 1s–30s range.
	pollBackoff = backoffConfig{
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
		maxRetries: 0,
		jitterPct:  0.25,
	}

	// submitBackoff is used for response submission retries: 3 retries, 1s–10s range.
	submitBackoff = backoffConfig{
		baseDelay:  1 * time.Second,
		maxDelay:   10 * time.Second,
		maxRetries: 3,
		jitterPct:  0.25,
	}
)
