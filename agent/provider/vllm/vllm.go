// Package vllm provides a provider for vLLM model servers.
// It delegates to the OpenAI provider since vLLM exposes an OpenAI-compatible API.
//
// The server address is read from VLLM_BASE_URL, defaulting to "http://localhost:8000/v1".
//
// Usage:
//
//	provider, err := vllm.New("mistralai/Mistral-7B-Instruct-v0.2")
//	provider, err := vllm.New("meta-llama/Llama-3-8B-Instruct", vllm.WithBaseURL("http://10.0.0.5:8000/v1"))
package vllm

import (
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent/provider/openai"
)

// Provider is the provider type returned by constructors in this package.
type Provider = openai.OpenAIProvider

// Option configures the vLLM provider.
type Option = openai.Option

// Provider options are re-exported from the shared OpenAI-compatible adapter.
var (
	WithCapabilities           = openai.WithCapabilities
	WithContextWindowTokens    = openai.WithContextWindowTokens
	WithMaxOutputTokens        = openai.WithMaxOutputTokens
	WithToolUse                = openai.WithToolUse
	WithToolChoice             = openai.WithToolChoice
	WithToolChoiceAuto         = openai.WithToolChoiceAuto
	WithToolChoiceRequired     = openai.WithToolChoiceRequired
	WithToolChoiceSpecific     = openai.WithToolChoiceSpecific
	WithNativeStructuredOutput = openai.WithNativeStructuredOutput
)

// WithBaseURL overrides the vLLM server URL. By default, the URL is read from
// VLLM_BASE_URL, falling back to http://localhost:8000/v1. The URL is normalized
// to a single /v1 suffix, so a value with or without /v1 behaves the same.
func WithBaseURL(url string) Option {
	return openai.WithBaseURL(normalizeBaseURL(url))
}

// New creates a provider targeting a vLLM server.
// The model parameter is the HuggingFace model ID (e.g. "mistralai/Mistral-7B-Instruct-v0.2").
func New(model string, opts ...Option) (*Provider, error) {
	baseURL := os.Getenv("VLLM_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8000/v1"
	}
	base := []Option{openai.WithBaseURL(normalizeBaseURL(baseURL))}
	return openai.NewCompatible(model, append(base, opts...)...)
}

// normalizeBaseURL trims trailing slashes and any existing /v1 suffix, then
// appends a single /v1, so a host with or without /v1 yields one /v1.
func normalizeBaseURL(host string) string {
	s := strings.TrimRight(host, "/")
	s = strings.TrimSuffix(s, "/v1")
	s = strings.TrimRight(s, "/")
	return s + "/v1"
}

// Must is a helper that wraps a call to New and panics on error.
func Must(p *Provider, err error) *Provider {
	if err != nil {
		panic("vllm: " + err.Error())
	}
	return p
}
