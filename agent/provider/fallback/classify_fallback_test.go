package fallback_test

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStream_StopsOnClassifiedNonRetryableError verifies the chain does NOT
// advance past a classified non-retryable ProviderError (e.g. a 400 or 401):
// another provider cannot fix a malformed request or an auth failure.
func TestStream_StopsOnClassifiedNonRetryableError(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		primary := failProvider(&agent.ProviderError{
			Cause:      errAny,
			StatusCode: status,
			Retryable:  false,
		})
		backup := okProvider("from backup")
		p := fallback.New(primary, backup)

		resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

		require.Error(t, err, "status %d should stop the chain", status)
		assert.Nil(t, resp)
		assert.Equal(t, 1, primary.calls)
		assert.Zero(t, backup.calls, "status %d must not advance to backup", status)
	}
}

// TestStream_AdvancesOnClassifiedRetryableError verifies the chain advances on
// a classified retryable ProviderError (e.g. a 429 or 503).
func TestStream_AdvancesOnClassifiedRetryableError(t *testing.T) {
	for _, status := range []int{429, 500, 503} {
		primary := failProvider(&agent.ProviderError{
			Cause:      errAny,
			StatusCode: status,
			Retryable:  true,
		})
		backup := okProvider("from backup")
		p := fallback.New(primary, backup)

		resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

		require.NoError(t, err, "status %d should fall back", status)
		assert.Equal(t, "from backup", resp.Text)
		assert.Equal(t, 1, backup.calls)
	}
}

// TestStream_AdvancesOnUnclassifiedError verifies that an error with no
// retryability classification still advances (the conservative default).
func TestStream_AdvancesOnUnclassifiedError(t *testing.T) {
	// A bare ProviderError (no StatusCode) is unclassified.
	primary := failProvider(&agent.ProviderError{Cause: errAny})
	backup := okProvider("from backup")
	p := fallback.New(primary, backup)

	resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

	require.NoError(t, err)
	assert.Equal(t, "from backup", resp.Text)
	assert.Equal(t, 1, backup.calls)
}

var errAny = assertError("boom")

type assertError string

func (e assertError) Error() string { return string(e) }
