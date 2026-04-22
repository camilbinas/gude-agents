// Package vllm provides a provider for vLLM model servers.
// It delegates to the OpenAI provider since vLLM exposes an OpenAI-compatible API.
//
// The server address is read from VLLM_BASE_URL, defaulting to "http://localhost:8000/v1".
//
// Usage:
//
//	provider, err := vllm.New("mistralai/Mistral-7B-Instruct-v0.2")
//	provider, err := vllm.New("meta-llama/Llama-3-8B-Instruct", vllm.WithBaseURL("http://10.0.0.5:8000/v1"))
//
// Documented in docs/providers/vllm.md — update when changing constructor, options, or behavior.
package vllm

import (
	"os"

	"github.com/camilbinas/gude-agents/agent/provider/openai"
)

// VLLMProvider is the provider type returned by constructors in this package.
type VLLMProvider = openai.OpenAIProvider

// Option configures the vLLM provider.
type Option = openai.Option

// WithBaseURL overrides the vLLM server URL.
// By default, the URL is read from VLLM_BASE_URL, falling back to http://localhost:8000/v1.
var WithBaseURL = openai.WithBaseURL

// WithMaxTokens sets the max tokens for responses.
var WithMaxTokens = openai.WithMaxTokens

// New creates a provider targeting a vLLM server.
// The model parameter is the HuggingFace model ID (e.g. "mistralai/Mistral-7B-Instruct-v0.2").
func New(model string, opts ...Option) (*VLLMProvider, error) {
	baseURL := os.Getenv("VLLM_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8000/v1"
	}
	base := []Option{openai.WithBaseURL(baseURL)}
	return openai.New(model, append(base, opts...)...)
}

// Must is a helper that wraps a call to New and panics on error.
func Must(p *VLLMProvider, err error) *VLLMProvider {
	if err != nil {
		panic("vllm: " + err.Error())
	}
	return p
}
