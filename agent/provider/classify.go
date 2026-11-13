package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// RetryableStatus reports whether an HTTP status code is retryable: 429 and any
// 5xx. Every other code (including a zero/unknown status) returns false.
func RetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// RetryableNetworkError reports whether err is a retryable transport error.
// net.Error / net.OpError failures are retryable; context cancellation,
// deadline-exceeded, and a nil error are not.
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

// ParseRetryAfter parses an HTTP Retry-After header in either delta-seconds or
// HTTP-date form. Returns 0 when empty, unparseable, or in the past.
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
