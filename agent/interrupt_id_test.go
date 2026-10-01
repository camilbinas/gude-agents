package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

var errTestRNG = errors.New("rng unavailable")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errTestRNG }

var hex128 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestNewInterruptID_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		id, err := newInterruptID(nil) // nil selects crypto/rand.Reader
		if err != nil {
			t.Fatal(err)
		}
		if !hex128.MatchString(id) {
			t.Fatalf("id %q is not 32 lowercase hex characters", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate interrupt ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewInterruptID_DeterministicReader(t *testing.T) {
	id, err := newInterruptID(bytes.NewReader(bytes.Repeat([]byte{0xab}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if id != "abababababababababababababababab" {
		t.Fatalf("id = %q", id)
	}
}

func TestNewInterruptID_ReaderFailures(t *testing.T) {
	cases := map[string]io.Reader{
		"error":      failingReader{},
		"short read": bytes.NewReader(make([]byte, 8)),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			id, err := newInterruptID(r)
			if err == nil {
				t.Fatalf("newInterruptID = %q, want error", id)
			}
			if id != "" {
				t.Fatalf("id = %q, want empty on failure", id)
			}
		})
	}
	if _, err := newInterruptID(failingReader{}); !errors.Is(err, errTestRNG) {
		t.Fatalf("error = %v, want wrapped RNG error", err)
	}
}

// An RNG failure while pausing must fail the invocation before anything is
// persisted: no conversation commit and no interrupt registration.
func TestInterrupt_RNGFailurePersistsNothing(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"ORD-1"}`),
		}}},
	)
	conversations := newTestMemoryStore()
	interrupts := newMemoryInterruptStore()
	a, err := New(provider, "helpful",
		WithTools(deleteOrderTool()),
		WithConversationStore(conversations),
		WithInterruptStore(interrupts),
	)
	if err != nil {
		t.Fatal(err)
	}
	a.random = failingReader{}

	res, err := a.Invoke(Background().WithConversationID("conv-1"), "delete order 1")
	if !errors.Is(err, errTestRNG) {
		t.Fatalf("Invoke error = %v, want RNG failure", err)
	}
	if res.Interrupt != nil || res.StopReason == StopInterrupt {
		t.Fatalf("result = %+v, want no interrupt", res)
	}
	if len(interrupts.pending) != 0 {
		t.Fatalf("pending interrupts = %v, want none", interrupts.pending)
	}
	conversations.mu.Lock()
	defer conversations.mu.Unlock()
	if snap, ok := conversations.data["conv-1"]; ok {
		t.Fatalf("conversation saved = %+v, want no commit", snap)
	}
}
