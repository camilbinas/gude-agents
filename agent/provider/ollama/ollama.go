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
package ollama

import (
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent/provider/openai"
)

// Provider is the provider type returned by constructors in this package.
type Provider = openai.OpenAIProvider

// Option configures the Ollama provider.
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

// WithBaseURL overrides the Ollama server URL. By default, the URL is read from
// OLLAMA_HOST, falling back to http://localhost:11434. The URL is normalized to
// a single /v1 suffix, so a value with or without /v1 behaves the same.
func WithBaseURL(url string) Option {
	return openai.WithBaseURL(normalizeBaseURL(url))
}

// New creates a provider targeting a local Ollama server.
// The model parameter is the Ollama model name (e.g. "llama3.2", "qwen2.5", "mistral").
// Known names receive Gude's best-known capability metadata; any other name is
// accepted and reports only the adapter baseline. Options override both.
func New(model string, opts ...Option) (*Provider, error) {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	base := []Option{openai.WithBaseURL(normalizeBaseURL(host))}
	// Known models start from the catalog; caller options follow and win.
	if profile, ok := catalog[model]; ok {
		base = append(base, openai.WithCapabilities(profile.capabilities))
	}
	return openai.NewCompatible(model, append(base, opts...)...)
}

// Must is a helper that wraps a call to New and panics on error.
func Must(p *Provider, err error) *Provider {
	if err != nil {
		panic("ollama: " + err.Error())
	}
	return p
}

// normalizeBaseURL trims trailing slashes and any existing /v1 suffix, then
// appends a single /v1, so a host with or without /v1 yields one /v1.
func normalizeBaseURL(host string) string {
	s := strings.TrimRight(host, "/")
	s = strings.TrimSuffix(s, "/v1")
	s = strings.TrimRight(s, "/")
	return s + "/v1"
}
