package registry

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// fakeProvider is a minimal provider for testing.
type fakeProvider struct{ name string }

func (f *fakeProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	return &agent.ProviderResponse{Text: f.name}, nil
}
func (f *fakeProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, _ agent.StreamCallback) (*agent.ProviderResponse, error) {
	return &agent.ProviderResponse{Text: f.name}, nil
}

func TestRegisterAndNew(t *testing.T) {
	// Clean state for this test.
	mu.Lock()
	old := registry
	registry = make(map[string]entry)
	mu.Unlock()
	defer func() {
		mu.Lock()
		registry = old
		mu.Unlock()
	}()

	Register("test",
		func() (agent.Provider, error) { return &fakeProvider{name: "cheap"}, nil },
		func() (agent.Provider, error) { return &fakeProvider{name: "standard"}, nil },
		func() (agent.Provider, error) { return &fakeProvider{name: "smart"}, nil },
	)

	tests := []struct {
		tier Tier
		want string
	}{
		{Cheapest, "cheap"},
		{Standard, "standard"},
		{Smartest, "smart"},
	}

	for _, tt := range tests {
		p, err := New("test", tt.tier)
		if err != nil {
			t.Fatalf("New(%q, %q): %v", "test", tt.tier, err)
		}
		resp, _ := p.Converse(context.Background(), agent.ConverseParams{})
		if resp.Text != tt.want {
			t.Errorf("tier %q: got %q, want %q", tt.tier, resp.Text, tt.want)
		}
	}
}

func TestNew_UnknownProvider(t *testing.T) {
	_, err := New("nonexistent", Standard)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNew_UnknownTier(t *testing.T) {
	mu.Lock()
	old := registry
	registry = make(map[string]entry)
	mu.Unlock()
	defer func() {
		mu.Lock()
		registry = old
		mu.Unlock()
	}()

	Register("test",
		func() (agent.Provider, error) { return &fakeProvider{}, nil },
		nil, nil,
	)

	_, err := New("test", "ultra")
	if err == nil {
		t.Fatal("expected error for unknown tier")
	}
}

func TestNew_NilTierFactory(t *testing.T) {
	mu.Lock()
	old := registry
	registry = make(map[string]entry)
	mu.Unlock()
	defer func() {
		mu.Lock()
		registry = old
		mu.Unlock()
	}()

	Register("partial", nil, nil,
		func() (agent.Provider, error) { return &fakeProvider{name: "smart"}, nil },
	)

	_, err := New("partial", Cheapest)
	if err == nil {
		t.Fatal("expected error for nil cheapest factory")
	}

	p, err := New("partial", Smartest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, _ := p.Converse(context.Background(), agent.ConverseParams{})
	if resp.Text != "smart" {
		t.Errorf("got %q, want %q", resp.Text, "smart")
	}
}

func TestNew_CaseInsensitive(t *testing.T) {
	mu.Lock()
	old := registry
	registry = make(map[string]entry)
	mu.Unlock()
	defer func() {
		mu.Lock()
		registry = old
		mu.Unlock()
	}()

	Register("MyProvider",
		func() (agent.Provider, error) { return &fakeProvider{name: "ok"}, nil },
		nil, nil,
	)

	p, err := New("MYPROVIDER", Cheapest)
	if err != nil {
		t.Fatalf("case-insensitive lookup failed: %v", err)
	}
	resp, _ := p.Converse(context.Background(), agent.ConverseParams{})
	if resp.Text != "ok" {
		t.Errorf("got %q, want %q", resp.Text, "ok")
	}
}

func TestNames(t *testing.T) {
	mu.Lock()
	old := registry
	registry = make(map[string]entry)
	mu.Unlock()
	defer func() {
		mu.Lock()
		registry = old
		mu.Unlock()
	}()

	Register("alpha", nil, nil, nil)
	Register("beta", nil, nil, nil)

	names := Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d: %v", len(names), names)
	}
}
