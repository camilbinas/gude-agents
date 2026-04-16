// Package registry provides a provider lookup table that maps a provider name
// and quality tier to a concrete agent.Provider. This enables environment-driven
// provider selection (e.g. MODEL_PROVIDER=bedrock MODEL_TIER=standard) without hardcoding
// provider imports in application code.
//
// Built-in providers register themselves via RegisterBuiltins. Third-party
// providers can register using the same Register function.
//
// Usage:
//
//	registry.RegisterBuiltins()
//	p, err := registry.New("bedrock", registry.Standard)
//
// Or from environment variables:
//
//	registry.RegisterBuiltins()
//	p, err := registry.FromEnv() // reads MODEL_PROVIDER and MODEL_TIER
package registry

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/gemini"
	"github.com/camilbinas/gude-agents/agent/provider/openai"
)

// Tier represents a model quality/cost tier.
type Tier string

const (
	Cheapest Tier = "cheapest"
	Standard Tier = "standard"
	Smartest Tier = "smartest"
)

// Factory creates a provider for a given tier.
type Factory func() (agent.Provider, error)

// entry holds the three tier factories for a single provider.
type entry struct {
	cheapest Factory
	standard Factory
	smartest Factory
}

var (
	mu       sync.RWMutex
	registry = make(map[string]entry)
)

// Register adds a provider with factories for each tier.
// Any factory can be nil if the provider doesn't support that tier.
// Calling Register with an existing name overwrites the previous entry.
func Register(name string, cheapest, standard, smartest Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[strings.ToLower(name)] = entry{
		cheapest: cheapest,
		standard: standard,
		smartest: smartest,
	}
}

// RegisterBuiltins registers the three built-in providers (bedrock, anthropic, openai)
// with their default tier mappings. Call this once at startup.
func RegisterBuiltins() {
	Register("bedrock",
		func() (agent.Provider, error) { return bedrock.Cheapest() },
		func() (agent.Provider, error) { return bedrock.Standard() },
		func() (agent.Provider, error) { return bedrock.Smartest() },
	)
	Register("anthropic",
		func() (agent.Provider, error) { return anthropic.Cheapest() },
		func() (agent.Provider, error) { return anthropic.Standard() },
		func() (agent.Provider, error) { return anthropic.Smartest() },
	)
	Register("openai",
		func() (agent.Provider, error) { return openai.Cheapest() },
		func() (agent.Provider, error) { return openai.Standard() },
		func() (agent.Provider, error) { return openai.Smartest() },
	)
	Register("gemini",
		func() (agent.Provider, error) { return gemini.Cheapest() },
		func() (agent.Provider, error) { return gemini.Standard() },
		func() (agent.Provider, error) { return gemini.Smartest() },
	)
}

// New creates a provider by name and tier.
// Returns an error if the name is not registered or the tier is not supported.
func New(name string, tier Tier) (agent.Provider, error) {
	mu.RLock()
	e, ok := registry[strings.ToLower(name)]
	mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("registry: unknown provider %q (registered: %s)", name, registered())
	}

	var factory Factory
	switch tier {
	case Cheapest:
		factory = e.cheapest
	case Standard:
		factory = e.standard
	case Smartest:
		factory = e.smartest
	default:
		return nil, fmt.Errorf("registry: unknown tier %q (supported: cheapest, standard, smartest)", tier)
	}

	if factory == nil {
		return nil, fmt.Errorf("registry: provider %q does not support tier %q", name, tier)
	}

	return factory()
}

// FromEnv reads the MODEL_PROVIDER and MODEL_TIER environment variables and creates a provider.
// Defaults to "bedrock" and "standard" if unset.
func FromEnv() (agent.Provider, error) {
	name := os.Getenv("MODEL_PROVIDER")
	if name == "" {
		name = "bedrock"
	}
	tier := os.Getenv("MODEL_TIER")
	if tier == "" {
		tier = "standard"
	}
	return New(name, Tier(strings.ToLower(tier)))
}

// Names returns all registered provider names.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// registered returns a comma-separated list of registered provider names for error messages.
func registered() string {
	names := Names()
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
