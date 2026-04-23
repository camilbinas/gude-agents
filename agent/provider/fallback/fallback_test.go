package fallback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Test doubles ──────────────────────────────────────────────────────────────

type stubProvider struct {
	err  error
	resp *agent.ProviderResponse
}

func (s *stubProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	return s.resp, s.err
}

func (s *stubProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if cb != nil && s.resp != nil && s.resp.Text != "" {
		cb(s.resp.Text)
	}
	return s.resp, nil
}

func okProvider(text string) *stubProvider {
	return &stubProvider{resp: &agent.ProviderResponse{Text: text, Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5}}}
}

func failProvider() *stubProvider {
	return &stubProvider{err: errors.New("service unavailable")}
}

// ── Converse ──────────────────────────────────────────────────────────────────

func TestConverse_PrimarySucceeds(t *testing.T) {
	p := fallback.New(okProvider("hello"), failProvider())
	resp, err := p.Converse(context.Background(), agent.ConverseParams{})
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Text)
}

func TestConverse_FallsBackOnPrimaryError(t *testing.T) {
	p := fallback.New(failProvider(), okProvider("from backup"))
	resp, err := p.Converse(context.Background(), agent.ConverseParams{})
	require.NoError(t, err)
	assert.Equal(t, "from backup", resp.Text)
}

func TestConverse_TriesAllFallbacks(t *testing.T) {
	p := fallback.New(failProvider(), failProvider(), okProvider("third"))
	resp, err := p.Converse(context.Background(), agent.ConverseParams{})
	require.NoError(t, err)
	assert.Equal(t, "third", resp.Text)
}

func TestConverse_AllFail_ReturnsError(t *testing.T) {
	p := fallback.New(failProvider(), failProvider())
	_, err := p.Converse(context.Background(), agent.ConverseParams{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all providers failed")
}

// ── ConverseStream ────────────────────────────────────────────────────────────

func TestConverseStream_PrimarySucceeds(t *testing.T) {
	p := fallback.New(okProvider("streamed"), failProvider())
	var got string
	resp, err := p.ConverseStream(context.Background(), agent.ConverseParams{}, func(chunk string) {
		got += chunk
	})
	require.NoError(t, err)
	assert.Equal(t, "streamed", got)
	assert.Equal(t, "streamed", resp.Text)
}

func TestConverseStream_FallsBackOnPrimaryError(t *testing.T) {
	p := fallback.New(failProvider(), okProvider("backup stream"))
	var got string
	_, err := p.ConverseStream(context.Background(), agent.ConverseParams{}, func(chunk string) {
		got += chunk
	})
	require.NoError(t, err)
	assert.Equal(t, "backup stream", got)
}

func TestConverseStream_AllFail_ReturnsError(t *testing.T) {
	p := fallback.New(failProvider(), failProvider())
	_, err := p.ConverseStream(context.Background(), agent.ConverseParams{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all providers failed")
}

// ── Single provider edge case ─────────────────────────────────────────────────

func TestSingleProvider_Success(t *testing.T) {
	p := fallback.New(okProvider("only one"))
	resp, err := p.Converse(context.Background(), agent.ConverseParams{})
	require.NoError(t, err)
	assert.Equal(t, "only one", resp.Text)
}

func TestSingleProvider_Failure(t *testing.T) {
	p := fallback.New(failProvider())
	_, err := p.Converse(context.Background(), agent.ConverseParams{})
	require.Error(t, err)
}
