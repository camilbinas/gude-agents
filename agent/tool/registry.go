package tool

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is a concurrency-safe dynamic set of tools. Its zero value is ready to use.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// Register validates and adds a tool. Names must be unique.
func (r *Registry) Register(t Tool) error {
	if err := t.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	if _, exists := r.tools[t.Spec.Name]; exists {
		return fmt.Errorf("duplicate tool name: %q", t.Spec.Name)
	}
	r.tools[t.Spec.Name] = t
	return nil
}

// Unregister removes the named tool and reports whether it existed.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; !exists {
		return false
	}
	delete(r.tools, name)
	return true
}

// Lookup returns the named tool and whether it exists.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns a deterministic snapshot sorted by tool name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec.Name < out[j].Spec.Name })
	return out
}
