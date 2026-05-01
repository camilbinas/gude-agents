package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// structKey is a composite key type used to test struct keys in the KV store.
type structKey struct {
	Namespace string
	ID        int
}

// genKey generates a random key: string, int, or structKey.
func genKey(t *rapid.T, label string) any {
	kind := rapid.IntRange(0, 2).Draw(t, label+"_kind")
	switch kind {
	case 0:
		return rapid.String().Draw(t, label+"_str")
	case 1:
		return rapid.Int().Draw(t, label+"_int")
	default:
		return structKey{
			Namespace: rapid.StringMatching(`[a-z]{1,10}`).Draw(t, label+"_ns"),
			ID:        rapid.IntRange(0, 1000).Draw(t, label+"_id"),
		}
	}
}

// genValue generates a random value: string, int, bool, or nil.
func genValue(t *rapid.T, label string) any {
	kind := rapid.IntRange(0, 3).Draw(t, label+"_kind")
	switch kind {
	case 0:
		return rapid.String().Draw(t, label+"_str")
	case 1:
		return rapid.Int().Draw(t, label+"_int")
	case 2:
		return rapid.Bool().Draw(t, label+"_bool")
	default:
		return nil
	}
}

// TestProperty_KeyValueStoreRoundTrip verifies that for any key and value,
// Set then Get returns the same value with ok=true, and unset keys return (nil, false).
//
// **Validates: Requirements 1.2, 1.3**
func TestProperty_KeyValueStoreRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		// Generate a random number of key-value pairs to set (1–20)
		n := rapid.IntRange(1, 20).Draw(rt, "numPairs")

		type kv struct {
			key   any
			value any
		}
		pairs := make([]kv, n)

		for i := 0; i < n; i++ {
			k := genKey(rt, "key")
			v := genValue(rt, "val")
			pairs[i] = kv{key: k, value: v}
			c.Set(k, v)
		}

		// Verify all set keys return the correct value.
		// Note: if duplicate keys were generated, the last write wins.
		// Build a map of final expected values.
		expected := make(map[any]any)
		for _, p := range pairs {
			expected[p.key] = p.value
		}

		for k, expectedVal := range expected {
			got, ok := c.Get(k)
			if !ok {
				rt.Fatalf("Get(%v) returned ok=false, expected ok=true", k)
			}
			if got != expectedVal {
				rt.Fatalf("Get(%v) = %v, expected %v", k, got, expectedVal)
			}
		}

		// Verify an unset key returns (nil, false)
		unsetKey := genKey(rt, "unset")
		// Make sure the unset key is actually not in our expected map
		if _, exists := expected[unsetKey]; !exists {
			got, ok := c.Get(unsetKey)
			if ok {
				rt.Fatalf("Get(unset key %v) returned ok=true, expected ok=false", unsetKey)
			}
			if got != nil {
				rt.Fatalf("Get(unset key %v) = %v, expected nil", unsetKey, got)
			}
		}
	})
}

// TestProperty_KeyValueStoreUnsetKey verifies that Get on a fresh context
// always returns (nil, false) for any key.
//
// **Validates: Requirements 1.2, 1.3**
func TestProperty_KeyValueStoreUnsetKey(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		k := genKey(rt, "key")
		got, ok := c.Get(k)
		if ok {
			rt.Fatalf("Get(%v) on fresh context returned ok=true, expected ok=false", k)
		}
		if got != nil {
			rt.Fatalf("Get(%v) on fresh context = %v, expected nil", k, got)
		}
	})
}

// TestProperty_KeyValueStoreOverwrite verifies that setting the same key twice
// results in Get returning the last written value.
//
// **Validates: Requirements 1.2, 1.3**
func TestProperty_KeyValueStoreOverwrite(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		k := genKey(rt, "key")
		v1 := genValue(rt, "val1")
		v2 := genValue(rt, "val2")

		c.Set(k, v1)
		c.Set(k, v2)

		got, ok := c.Get(k)
		if !ok {
			rt.Fatalf("Get(%v) returned ok=false after overwrite", k)
		}
		if got != v2 {
			rt.Fatalf("Get(%v) = %v after overwrite, expected %v", k, got, v2)
		}
	})
}

// TestProperty_WithAccessorRoundTripAndPointerIdentity verifies that for any *Context,
// calling any With* method returns the same pointer, and the corresponding accessor
// returns the value that was set.
//
// **Validates: Requirements 1.5, 1.6, 1.7, 1.8, 1.9, 2.3**
func TestProperty_WithAccessorRoundTripAndPointerIdentity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		// Pick a random subset of With* methods to call (1–6)
		numCalls := rapid.IntRange(1, 6).Draw(rt, "numCalls")

		for i := 0; i < numCalls; i++ {
			method := rapid.IntRange(0, 5).Draw(rt, "method")
			switch method {
			case 0: // WithConversationID
				id := rapid.String().Draw(rt, "conversationID")
				ret := c.WithConversationID(id)
				if ret != c {
					rt.Fatalf("WithConversationID returned different pointer: got %p, want %p", ret, c)
				}
				if got := c.ConversationID(); got != id {
					rt.Fatalf("ConversationID() = %q, want %q", got, id)
				}

			case 1: // WithImages
				n := rapid.IntRange(0, 5).Draw(rt, "numImages")
				imgs := make([]ImageBlock, n)
				for j := range imgs {
					imgs[j] = ImageBlock{
						Source: ImageSource{
							Data:     rapid.SliceOfN(rapid.Byte(), 1, 100).Draw(rt, "imgData"),
							MIMEType: rapid.SampledFrom([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}).Draw(rt, "imgMIME"),
						},
					}
				}
				ret := c.WithImages(imgs)
				if ret != c {
					rt.Fatalf("WithImages returned different pointer: got %p, want %p", ret, c)
				}
				got := c.Images()
				if len(got) != len(imgs) {
					rt.Fatalf("Images() length = %d, want %d", len(got), len(imgs))
				}
				for j := range imgs {
					if got[j].Source.MIMEType != imgs[j].Source.MIMEType {
						rt.Fatalf("Images()[%d].Source.MIMEType = %q, want %q", j, got[j].Source.MIMEType, imgs[j].Source.MIMEType)
					}
				}

			case 2: // WithDocuments
				n := rapid.IntRange(0, 5).Draw(rt, "numDocs")
				docs := make([]DocumentBlock, n)
				for j := range docs {
					docs[j] = DocumentBlock{
						Source: DocumentSource{
							Data:     rapid.SliceOfN(rapid.Byte(), 1, 100).Draw(rt, "docData"),
							MIMEType: rapid.SampledFrom([]string{"application/pdf", "text/plain", "text/html", "text/csv", "text/markdown"}).Draw(rt, "docMIME"),
						},
					}
				}
				ret := c.WithDocuments(docs)
				if ret != c {
					rt.Fatalf("WithDocuments returned different pointer: got %p, want %p", ret, c)
				}
				got := c.Documents()
				if len(got) != len(docs) {
					rt.Fatalf("Documents() length = %d, want %d", len(got), len(docs))
				}
				for j := range docs {
					if got[j].Source.MIMEType != docs[j].Source.MIMEType {
						rt.Fatalf("Documents()[%d].Source.MIMEType = %q, want %q", j, got[j].Source.MIMEType, docs[j].Source.MIMEType)
					}
				}

			case 3: // WithInferenceConfig
				temp := rapid.Float64Range(0.0, 2.0).Draw(rt, "temperature")
				cfg := &InferenceConfig{Temperature: &temp}
				ret := c.WithInferenceConfig(cfg)
				if ret != c {
					rt.Fatalf("WithInferenceConfig returned different pointer: got %p, want %p", ret, c)
				}
				got := c.InferenceConfig()
				if got != cfg {
					rt.Fatalf("InferenceConfig() = %p, want %p", got, cfg)
				}
				if got.Temperature == nil || *got.Temperature != temp {
					rt.Fatalf("InferenceConfig().Temperature = %v, want %v", got.Temperature, temp)
				}

			case 4: // WithEventHook
				hook := BaseEventHook{}
				ret := c.WithEventHook(hook)
				if ret != c {
					rt.Fatalf("WithEventHook returned different pointer: got %p, want %p", ret, c)
				}
				got := c.EventHook()
				if got != hook {
					rt.Fatalf("EventHook() = %v, want %v", got, hook)
				}

			case 5: // WithIdentifier
				id := rapid.String().Draw(rt, "identifier")
				ret := c.WithIdentifier(id)
				if ret != c {
					rt.Fatalf("WithIdentifier returned different pointer: got %p, want %p", ret, c)
				}
				if got := c.Identifier(); got != id {
					rt.Fatalf("Identifier() = %q, want %q", got, id)
				}
			}
		}
	})
}

// TestProperty_ConcurrentKeyValueSafety verifies that concurrent Set/Get operations
// across N goroutines produce no data races (verified with -race) and that all final
// values are consistent with the last write for each key.
//
// **Validates: Requirements 12.1, 12.2**
func TestProperty_ConcurrentKeyValueSafety(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		// Generate number of goroutines (2–10)
		numGoroutines := rapid.IntRange(2, 10).Draw(rt, "numGoroutines")

		// Generate number of operations per goroutine (1–20)
		opsPerGoroutine := rapid.IntRange(1, 20).Draw(rt, "opsPerGoroutine")

		// Use a small key space to ensure contention on the same keys
		numKeys := rapid.IntRange(1, 5).Draw(rt, "numKeys")
		keys := make([]string, numKeys)
		for i := range keys {
			keys[i] = rapid.StringMatching(`[a-z]{1,5}`).Draw(rt, "key")
		}

		// Track the final expected value for each key.
		// We'll have each goroutine write its goroutine index as the value for a key.
		// After all goroutines complete, each key must hold a value that was written by
		// one of the goroutines (i.e., a valid goroutine index).
		type op struct {
			isSet bool
			key   string
			value int // goroutine index used as value for Set ops
		}

		// Pre-generate operations for each goroutine
		allOps := make([][]op, numGoroutines)
		for g := range allOps {
			ops := make([]op, opsPerGoroutine)
			for i := range ops {
				isSet := rapid.Bool().Draw(rt, "isSet")
				keyIdx := rapid.IntRange(0, numKeys-1).Draw(rt, "keyIdx")
				ops[i] = op{
					isSet: isSet,
					key:   keys[keyIdx],
					value: g, // use goroutine index as the written value
				}
			}
			allOps[g] = ops
		}

		// Execute all operations concurrently
		var wg sync.WaitGroup
		wg.Add(numGoroutines)
		for g := range allOps {
			go func(goroutineOps []op) {
				defer wg.Done()
				for _, o := range goroutineOps {
					if o.isSet {
						c.Set(o.key, o.value)
					} else {
						// Get should never panic; value may or may not be present
						c.Get(o.key)
					}
				}
			}(allOps[g])
		}
		wg.Wait()

		// After all goroutines complete, determine the set of keys that were written
		// and verify each one holds a valid value (one of the goroutine indices that wrote to it).
		writtenKeys := make(map[string]map[int]bool) // key -> set of goroutine indices that wrote to it
		for g, ops := range allOps {
			for _, o := range ops {
				if o.isSet {
					if writtenKeys[o.key] == nil {
						writtenKeys[o.key] = make(map[int]bool)
					}
					writtenKeys[o.key][g] = true
				}
			}
		}

		for key, validWriters := range writtenKeys {
			got, ok := c.Get(key)
			if !ok {
				rt.Fatalf("Get(%q) returned ok=false after concurrent writes", key)
			}
			gotInt, isInt := got.(int)
			if !isInt {
				rt.Fatalf("Get(%q) returned non-int value %v (%T)", key, got, got)
			}
			if !validWriters[gotInt] {
				rt.Fatalf("Get(%q) = %d, which is not a valid writer goroutine index (valid: %v)", key, gotInt, validWriters)
			}
		}
	})
}

// TestProperty_ParentCancellationPropagation verifies that when a parent context
// is cancelled, the *Context created via NewContext(parent) has Done() closed
// and Err() returns non-nil (context.Canceled).
//
// **Validates: Requirements 11.3**
func TestProperty_ParentCancellationPropagation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random delay before cancellation (0–500 microseconds)
		delayMicros := rapid.IntRange(0, 500).Draw(rt, "delayMicros")
		delay := time.Duration(delayMicros) * time.Microsecond

		// Create a cancellable parent context
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create the agent.Context wrapping the cancellable parent
		c := NewContext(parent)

		// Before cancellation, Err() should be nil
		if err := c.Err(); err != nil {
			rt.Fatalf("Err() before cancellation = %v, want nil", err)
		}

		// Wait the random delay, then cancel the parent
		if delay > 0 {
			time.Sleep(delay)
		}
		cancel()

		// After cancellation, Done() channel should be closed
		select {
		case <-c.Done():
			// expected: channel is closed
		case <-time.After(time.Second):
			rt.Fatalf("Done() channel not closed within 1s after parent cancellation")
		}

		// After cancellation, Err() should return non-nil (context.Canceled)
		if err := c.Err(); err == nil {
			rt.Fatalf("Err() after parent cancellation = nil, want non-nil")
		} else if err != context.Canceled {
			rt.Fatalf("Err() after parent cancellation = %v, want %v", err, context.Canceled)
		}
	})
}
