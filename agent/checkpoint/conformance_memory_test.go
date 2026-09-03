package checkpoint

import "testing"

// The in-memory store is the reference implementation for the shared contract.
func TestConformance_Memory(t *testing.T) {
	RunConformance(t, func(t *testing.T) Checkpointer {
		return NewMemory()
	})
}
