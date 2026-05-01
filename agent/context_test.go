package agent

import (
	"context"
	"testing"
	"time"
)

func TestNewContext_WrapsParent_Deadline(t *testing.T) {
	deadline := time.Now().Add(5 * time.Second)
	parent, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	c := NewContext(parent)

	got, ok := c.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	if !got.Equal(deadline) {
		t.Fatalf("expected deadline %v, got %v", deadline, got)
	}
}

func TestNewContext_WrapsParent_Values(t *testing.T) {
	type ctxKey struct{}
	parent := context.WithValue(context.Background(), ctxKey{}, "hello")

	c := NewContext(parent)

	v := c.Value(ctxKey{})
	if v != "hello" {
		t.Fatalf("expected parent value %q, got %v", "hello", v)
	}
}

func TestNewContext_WrapsParent_Cancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())

	c := NewContext(parent)

	cancel()

	select {
	case <-c.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("expected Done channel to close after parent cancel")
	}

	if c.Err() == nil {
		t.Fatal("expected non-nil Err after parent cancel")
	}
}

func TestBackground_EquivalentToNewContextBackground(t *testing.T) {
	c := Background()

	// Should have no deadline
	_, ok := c.Deadline()
	if ok {
		t.Fatal("Background() should not have a deadline")
	}

	// Should not be done
	select {
	case <-c.Done():
		t.Fatal("Background() should not be done")
	default:
		// expected
	}

	// Should have no error
	if c.Err() != nil {
		t.Fatalf("Background() Err should be nil, got %v", c.Err())
	}

	// Should have empty KV store
	_, ok = c.Get("anything")
	if ok {
		t.Fatal("Background() should have empty KV store")
	}
}

func TestNewContext_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil parent, got none")
		}
	}()

	NewContext(nil)
}

func TestWithConversationID_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()

	got := c.WithConversationID("conv-123")

	if got != c {
		t.Fatal("WithConversationID should return the same pointer")
	}
	if c.ConversationID() != "conv-123" {
		t.Fatalf("expected %q, got %q", "conv-123", c.ConversationID())
	}
}

func TestWithImages_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	imgs := []ImageBlock{
		{Source: ImageSource{MIMEType: "image/png", Data: []byte{0x89}}},
	}

	got := c.WithImages(imgs)

	if got != c {
		t.Fatal("WithImages should return the same pointer")
	}
	if len(c.Images()) != 1 {
		t.Fatalf("expected 1 image, got %d", len(c.Images()))
	}
	if c.Images()[0].Source.MIMEType != "image/png" {
		t.Fatalf("expected image/png, got %q", c.Images()[0].Source.MIMEType)
	}
}

func TestWithDocuments_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	docs := []DocumentBlock{
		{Source: DocumentSource{MIMEType: "application/pdf", Data: []byte{0x25}}},
	}

	got := c.WithDocuments(docs)

	if got != c {
		t.Fatal("WithDocuments should return the same pointer")
	}
	if len(c.Documents()) != 1 {
		t.Fatalf("expected 1 document, got %d", len(c.Documents()))
	}
	if c.Documents()[0].Source.MIMEType != "application/pdf" {
		t.Fatalf("expected application/pdf, got %q", c.Documents()[0].Source.MIMEType)
	}
}

func TestWithInferenceConfig_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	temp := 0.7
	cfg := &InferenceConfig{Temperature: &temp}

	got := c.WithInferenceConfig(cfg)

	if got != c {
		t.Fatal("WithInferenceConfig should return the same pointer")
	}
	if c.InferenceConfig() != cfg {
		t.Fatal("expected same InferenceConfig pointer")
	}
	if *c.InferenceConfig().Temperature != 0.7 {
		t.Fatalf("expected temperature 0.7, got %f", *c.InferenceConfig().Temperature)
	}
}

func TestWithEventHook_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	hook := BaseEventHook{}

	got := c.WithEventHook(hook)

	if got != c {
		t.Fatal("WithEventHook should return the same pointer")
	}
	if c.EventHook() == nil {
		t.Fatal("expected non-nil EventHook")
	}
}

func TestWithIdentifier_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()

	got := c.WithIdentifier("user-42")

	if got != c {
		t.Fatal("WithIdentifier should return the same pointer")
	}
	if c.Identifier() != "user-42" {
		t.Fatalf("expected %q, got %q", "user-42", c.Identifier())
	}
}

func TestSetGet_RoundTrip(t *testing.T) {
	c := Background()

	c.Set("key1", "value1")
	c.Set(42, true)

	v, ok := c.Get("key1")
	if !ok || v != "value1" {
		t.Fatalf("expected (value1, true), got (%v, %v)", v, ok)
	}

	v, ok = c.Get(42)
	if !ok || v != true {
		t.Fatalf("expected (true, true), got (%v, %v)", v, ok)
	}
}

func TestSetGet_Overwrite(t *testing.T) {
	c := Background()

	c.Set("key", "first")
	c.Set("key", "second")

	v, ok := c.Get("key")
	if !ok || v != "second" {
		t.Fatalf("expected (second, true), got (%v, %v)", v, ok)
	}
}

func TestGet_NonExistentKey(t *testing.T) {
	c := Background()

	v, ok := c.Get("missing")
	if ok || v != nil {
		t.Fatalf("expected (nil, false), got (%v, %v)", v, ok)
	}
}

func TestWithMethods_Chaining(t *testing.T) {
	c := Background()
	temp := 0.5
	cfg := &InferenceConfig{Temperature: &temp}
	hook := BaseEventHook{}

	result := c.
		WithConversationID("conv-1").
		WithIdentifier("user-1").
		WithInferenceConfig(cfg).
		WithEventHook(hook)

	if result != c {
		t.Fatal("chained With* methods should return the same pointer")
	}
	if c.ConversationID() != "conv-1" {
		t.Fatalf("expected conv-1, got %q", c.ConversationID())
	}
	if c.Identifier() != "user-1" {
		t.Fatalf("expected user-1, got %q", c.Identifier())
	}
	if c.InferenceConfig() != cfg {
		t.Fatal("expected same InferenceConfig pointer")
	}
	if c.EventHook() == nil {
		t.Fatal("expected non-nil EventHook")
	}
}

func TestContext_SatisfiesContextInterface(t *testing.T) {
	c := Background()

	// Verify *Context is assignable to context.Context
	var _ context.Context = c
}

func TestContext_UsageDefaultsToZero(t *testing.T) {
	c := Background()

	usage := c.Usage()
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("expected zero usage, got %+v", usage)
	}
}

func TestContext_SetUsage(t *testing.T) {
	c := Background()

	c.setUsage(TokenUsage{InputTokens: 100, OutputTokens: 50})

	usage := c.Usage()
	if usage.InputTokens != 100 {
		t.Fatalf("expected InputTokens=100, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Fatalf("expected OutputTokens=50, got %d", usage.OutputTokens)
	}
}
