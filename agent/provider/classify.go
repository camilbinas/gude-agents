package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// RetryableStatus reports whether an HTTP status code names a condition that a
// retry or provider failover can plausibly recover from: 429 (rate limited) and
// any 5xx server error are retryable; every other code (including client errors
// such as 400/401/403/404) is not. A zero status is treated as "unknown" and
// reported as not retryable by this helper alone — callers classify transport
// errors separately via RetryableNetworkError.
func RetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// RetryableNetworkError reports whether err is a transport-level error that is
// worth retrying: context cancellation and deadline-exceeded are never
// retryable (no retry can succeed under a done context), while timeouts and
// other net.Error / net.OpError transport failures are. It returns false for a
// nil error.
func RetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// ParseRetryAfter parses an HTTP Retry-After header value, supporting both the
// delta-seconds form ("120") and the HTTP-date form. It returns 0 when the
// value is empty or unparseable, or when the date is in the past.
func ParseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// RetryAfterFromResponse extracts the Retry-After hint from an HTTP response's
// headers, returning 0 when the response is nil or carries no usable value.
func RetryAfterFromResponse(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	return ParseRetryAfter(resp.Header.Get("Retry-After"))
}
