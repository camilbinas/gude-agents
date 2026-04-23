package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"pgregory.net/rapid"
)

// Feature: image-input, Property 1: MIME type validation is exact
//
// TestProperty_MIMETypeValidationExact verifies that for any string s,
// ImageSource{MIMEType: s}.Validate() returns nil if and only if s is one of
// "image/jpeg", "image/png", "image/gif", or "image/webp".
//
// **Validates: Requirements 1.4, 4.1, 4.2**
func TestProperty_MIMETypeValidationExact(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.String().Draw(rt, "mimeType")
		err := ImageSource{MIMEType: s}.Validate()

		isValid := validMIMETypes[s]
		if isValid && err != nil {
			rt.Fatalf("Validate() returned error for valid MIME type %q: %v", s, err)
		}
		if !isValid && err == nil {
			rt.Fatalf("Validate() returned nil for invalid MIME type %q", s)
		}
	})
}

// Feature: image-input, Property 2: Invalid MIME type error identifies the bad value
//
// TestProperty_InvalidMIMETypeErrorContainsValue verifies that for any string s
// that is not a valid MIME type, the error returned by Validate() contains s as
// a substring.
//
// **Validates: Requirements 4.2**
func TestProperty_InvalidMIMETypeErrorContainsValue(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.String().Filter(func(s string) bool {
			return !validMIMETypes[s]
		}).Draw(rt, "invalidMIMEType")

		err := ImageSource{MIMEType: s}.Validate()
		if err == nil {
			rt.Fatalf("Validate() returned nil for invalid MIME type %q", s)
		}
		if !containsString(err.Error(), s) {
			rt.Fatalf("error %q does not contain invalid MIME type %q", err.Error(), s)
		}
	})
}

// containsString reports whether substr is contained in s.
func containsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// genValidImageBlock generates a random ImageBlock with a valid MIME type.
func genValidImageBlock(t *rapid.T, name string) ImageBlock {
	mimeTypes := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
	mime := mimeTypes[rapid.IntRange(0, 3).Draw(t, name+"_mime")]
	data := rapid.SliceOf(rapid.Byte()).Draw(t, name+"_data")
	return ImageBlock{Source: ImageSource{MIMEType: mime, Data: data}}
}

// Feature: image-input, Property 5: Loop prepends all images to the first user message
//
// TestProperty_LoopPrependsImagesToFirstUserMessage verifies that for any non-empty
// []ImageBlock slice attached via WithImages, after the agent loop builds the first
// user Message, the Content slice starts with those ImageBlock values in the same
// order, followed by the TextBlock.
//
// **Validates: Requirements 3.1**
func TestProperty_LoopPrependsImagesToFirstUserMessage(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a non-empty slice of valid ImageBlocks.
		n := rapid.IntRange(1, 5).Draw(rt, "imageCount")
		images := make([]ImageBlock, n)
		for i := range n {
			images[i] = genValidImageBlock(rt, "img")
		}

		// Generate an arbitrary user message string.
		msg := rapid.String().Draw(rt, "msg")

		// Create a capturing provider that returns a simple text response.
		cp := newCapturingProvider(&ProviderResponse{Text: "ok"})
		a, err := New(cp, prompt.Text("sys"), nil)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Invoke with images attached to the context.
		ctx := WithImages(context.Background(), images)
		_, _, invokeErr := a.Invoke(ctx, msg)
		if invokeErr != nil {
			rt.Fatalf("unexpected error: %v", invokeErr)
		}

		// The provider must have been called at least once.
		if len(cp.captured) == 0 {
			rt.Fatal("provider was never called")
		}

		firstMsg := cp.captured[0].Messages[0]
		content := firstMsg.Content

		// Content must have len(images)+1 blocks.
		if len(content) != len(images)+1 {
			rt.Fatalf("expected %d content blocks, got %d", len(images)+1, len(content))
		}

		// First len(images) blocks must equal the generated image slice.
		for i, img := range images {
			got, ok := content[i].(ImageBlock)
			if !ok {
				rt.Fatalf("content[%d]: expected ImageBlock, got %T", i, content[i])
			}
			if !reflect.DeepEqual(got, img) {
				rt.Fatalf("content[%d]: expected %+v, got %+v", i, img, got)
			}
		}

		// Last block must be the TextBlock with the user message.
		last := content[len(content)-1]
		tb, ok := last.(TextBlock)
		if !ok {
			rt.Fatalf("last content block: expected TextBlock, got %T", last)
		}
		if tb.Text != msg {
			rt.Fatalf("TextBlock.Text: expected %q, got %q", msg, tb.Text)
		}
	})
}

// Feature: image-input, Property 3: Image bytes round-trip through base64
//
// TestProperty_ImageBytesRoundTripThroughBase64 verifies that for any byte slice b,
// base64-encoding b to produce a string s, then base64-decoding s, produces a byte
// slice equal to b.
//
// **Validates: Requirements 11.1**
func TestProperty_ImageBytesRoundTripThroughBase64(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		b := rapid.SliceOf(rapid.Byte()).Draw(rt, "bytes")

		encoded := base64.StdEncoding.EncodeToString(b)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			rt.Fatalf("unexpected base64 decode error: %v", err)
		}

		// For nil/empty slices, treat both as equivalent to empty.
		if len(b) == 0 && len(decoded) == 0 {
			return
		}
		if !bytes.Equal(decoded, b) {
			rt.Fatalf("round-trip mismatch: original %v, got %v", b, decoded)
		}
	})
}

// Feature: image-input, Property 4: Base64 string round-trip
//
// TestProperty_Base64StringRoundTrip verifies that for any valid base64 string s
// (produced by encoding some byte slice), decoding s to bytes and re-encoding to
// base64 produces a string equal to s.
//
// **Validates: Requirements 11.2**
func TestProperty_Base64StringRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a valid base64 string by encoding an arbitrary byte slice.
		b := rapid.SliceOf(rapid.Byte()).Draw(rt, "bytes")
		s := base64.StdEncoding.EncodeToString(b)

		// Decode s back to bytes.
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			rt.Fatalf("unexpected base64 decode error: %v", err)
		}

		// Re-encode to base64.
		reEncoded := base64.StdEncoding.EncodeToString(decoded)

		if reEncoded != s {
			rt.Fatalf("round-trip mismatch: original %q, got %q", s, reEncoded)
		}
	})
}

// Feature: image-input, Property 6: Loop with images persists ImageBlocks in memory
//
// TestProperty_LoopWithImagesPersistsImageBlocksInMemory verifies that for any
// non-empty []ImageBlock slice attached via WithImages, when the agent loop
// completes successfully with memory enabled, the saved conversation history
// contains a user message whose Content slice includes those ImageBlock values.
//
// **Validates: Requirements 3.4**
func TestProperty_LoopWithImagesPersistsImageBlocksInMemory(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a non-empty slice of valid ImageBlocks.
		n := rapid.IntRange(1, 5).Draw(rt, "imageCount")
		images := make([]ImageBlock, n)
		for i := range n {
			images[i] = genValidImageBlock(rt, "img")
		}

		// Set up an in-memory store and a scripted provider that returns a simple text response.
		store := newInMemoryStore()
		sp := newScriptedProvider(&ProviderResponse{Text: "ok"})

		a, err := New(sp, prompt.Text("sys"), nil, WithMemory(store, "conv-pbt"))
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Invoke with images attached to the context.
		ctx := WithImages(context.Background(), images)
		_, _, invokeErr := a.Invoke(ctx, "hello")
		if invokeErr != nil {
			rt.Fatalf("unexpected error: %v", invokeErr)
		}

		// Load the saved messages from the store.
		saved, loadErr := store.Load(context.Background(), "conv-pbt")
		if loadErr != nil {
			rt.Fatalf("failed to load messages: %v", loadErr)
		}
		if len(saved) == 0 {
			rt.Fatal("no messages were saved to memory")
		}

		// Find the user message and verify it contains the ImageBlock values.
		var userMsg *Message
		for i := range saved {
			if saved[i].Role == RoleUser {
				userMsg = &saved[i]
				break
			}
		}
		if userMsg == nil {
			rt.Fatal("no user message found in saved history")
		}

		// The user message Content must contain all generated ImageBlocks.
		if len(userMsg.Content) < len(images)+1 {
			rt.Fatalf("expected at least %d content blocks, got %d", len(images)+1, len(userMsg.Content))
		}

		for i, img := range images {
			got, ok := userMsg.Content[i].(ImageBlock)
			if !ok {
				rt.Fatalf("content[%d]: expected ImageBlock, got %T", i, userMsg.Content[i])
			}
			if !reflect.DeepEqual(got, img) {
				rt.Fatalf("content[%d]: expected %+v, got %+v", i, img, got)
			}
		}
	})
}
