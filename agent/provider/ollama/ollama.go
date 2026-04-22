// Package ollama provides a provider for local Ollama model servers.
// It delegates to the OpenAI provider since Ollama exposes an OpenAI-compatible API.
//
// The server address is read from OLLAMA_HOST (e.g. "http://my-host:11434"),
// defaulting to "http://localhost:11434". The "/v1" path is appended automatically.
//
// Usage:
//
//	provider, err := ollama.New("qwen2.5")
//	provider, err := ollama.New("llama3.2", ollama.WithBaseURL("http://10.0.0.5:11434/v1"))
//
// Documented in docs/providers/ollama.md — update when changing constructor, options, or behavior.
package ollama

import (
	"os"

	"github.com/camilbinas/gude-agents/agent/provider/openai"
)

// OllamaProvider is the provider type returned by constructors in this package.
type OllamaProvider = openai.OpenAIProvider

// Option configures the Ollama provider.
type Option = openai.Option

// WithBaseURL overrides the Ollama server URL.
// By default, the URL is read from OLLAMA_HOST, falling back to http://localhost:11434.
var WithBaseURL = openai.WithBaseURL

// WithMaxTokens sets the max tokens for responses.
var WithMaxTokens = openai.WithMaxTokens

// New creates a provider targeting a local Ollama server.
// The model parameter is the Ollama model name (e.g. "llama3.2", "qwen2.5", "mistral").
func New(model string, opts ...Option) (*OllamaProvider, error) {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	base := []Option{openai.WithBaseURL(host + "/v1")}
	return openai.New(model, append(base, opts...)...)
}

// Must is a helper that wraps a call to New and panics on error.
func Must(p *OllamaProvider, err error) *OllamaProvider {
	if err != nil {
		panic("ollama: " + err.Error())
	}
	return p
}
