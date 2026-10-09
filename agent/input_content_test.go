package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// TestImageSourceValidate_ValidMIMETypes verifies that each of the four
// supported MIME types returns nil from Validate().
func TestImageSourceValidate_ValidMIMETypes(t *testing.T) {
	validTypes := []string{
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/webp",
	}
	for _, mime := range validTypes {
		t.Run(mime, func(t *testing.T) {
			src := ImageSource{Data: []byte{0xFF}, MIMEType: mime}
			if err := src.Validate(); err != nil {
				t.Errorf("Validate() returned unexpected error for %q: %v", mime, err)
			}
		})
	}
}

// TestImageSourceValidate_InvalidMIMEType verifies that an invalid MIME type
// returns a non-nil error that contains the invalid string.
func TestImageSourceValidate_InvalidMIMEType(t *testing.T) {
	invalidTypes := []string{
		"image/bmp",
		"image/tiff",
		"application/pdf",
		"text/plain",
		"",
		"image/",
		"jpeg",
	}
	for _, mime := range invalidTypes {
		t.Run(mime, func(t *testing.T) {
			src := ImageSource{Data: []byte{0xFF}, MIMEType: mime}
			err := src.Validate()
			if err == nil {
				t.Errorf("Validate() returned nil for invalid MIME type %q, expected error", mime)
				return
			}
			if !strings.Contains(err.Error(), mime) {
				t.Errorf("error %q does not contain invalid MIME type %q", err.Error(), mime)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Loop image propagation tests
// ---------------------------------------------------------------------------

// TestLoop_NoImages_SingleTextBlock verifies that when no images are attached,
// the first user message contains exactly one TextBlock (backward compatibility).
func TestLoop_NoImages_SingleTextBlock(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "ok"})
	a, err := New(cp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) == 0 {
		t.Fatal("provider was never called")
	}

	firstMsg := cp.captured[0].Messages[0]
	if len(firstMsg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(firstMsg.Content))
	}
	tb, ok := firstMsg.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", firstMsg.Content[0])
	}
	if tb.Text != "hello" {
		t.Errorf("expected TextBlock.Text=%q, got %q", "hello", tb.Text)
	}
}

// TestLoop_WithImages_PrependsImagesThenText verifies that when images are
// attached via WithImages, the first user message content is [ImageBlock,
// ImageBlock, TextBlock].
func TestLoop_WithImages_PrependsImagesThenText(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "ok"})
	a, err := New(cp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	img1 := ImageBlock{Source: ImageSource{MIMEType: "image/jpeg", Data: []byte{0xFF, 0xD8}}}
	img2 := ImageBlock{Source: ImageSource{MIMEType: "image/png", Data: []byte{0x89, 0x50}}}
	images := []ImageBlock{img1, img2}

	ctx := Background().WithImages(images)
	_, err = a.Invoke(ctx, "describe these")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) == 0 {
		t.Fatal("provider was never called")
	}

	firstMsg := cp.captured[0].Messages[0]
	content := firstMsg.Content

	// Expect [ImageBlock, ImageBlock, TextBlock].
	if len(content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(content))
	}

	got1, ok := content[0].(ImageBlock)
	if !ok {
		t.Fatalf("content[0]: expected ImageBlock, got %T", content[0])
	}
	if !reflect.DeepEqual(got1, img1) {
		t.Errorf("content[0]: expected %+v, got %+v", img1, got1)
	}

	got2, ok := content[1].(ImageBlock)
	if !ok {
		t.Fatalf("content[1]: expected ImageBlock, got %T", content[1])
	}
	if !reflect.DeepEqual(got2, img2) {
		t.Errorf("content[1]: expected %+v, got %+v", img2, got2)
	}

	tb, ok := content[2].(TextBlock)
	if !ok {
		t.Fatalf("content[2]: expected TextBlock, got %T", content[2])
	}
	if tb.Text != "describe these" {
		t.Errorf("TextBlock.Text: expected %q, got %q", "describe these", tb.Text)
	}
}

// panicProvider panics if called — used to verify the provider is never reached.
type panicProvider struct{}

func (panicProvider) Name() string { return "mock" }

func (panicProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	panic("panicProvider.Stream called — should not have reached provider")
}

// TestLoop_InvalidMIMEType_ReturnsErrorBeforeProvider verifies that when an
// ImageBlock with an invalid MIME type is attached, the loop returns an error
// before calling the provider.
func TestLoop_InvalidMIMEType_ReturnsErrorBeforeProvider(t *testing.T) {
	a, err := New(panicProvider{}, "sys")
	if err != nil {
		t.Fatal(err)
	}

	badImage := ImageBlock{Source: ImageSource{MIMEType: "image/bmp", Data: []byte{0x42, 0x4D}}}
	ctx := Background().WithImages([]ImageBlock{badImage})

	_, invokeErr := a.Invoke(ctx, "hello")
	if invokeErr == nil {
		t.Fatal("expected error for invalid MIME type, got nil")
	}
	if !strings.Contains(invokeErr.Error(), "image/bmp") {
		t.Errorf("expected error to contain %q, got: %v", "image/bmp", invokeErr)
	}
}

// TestProperty_MIMETypeValidationExact verifies that for any string s,
// ImageSource{MIMEType: s}.Validate() returns nil if and only if s is one of
// "image/jpeg", "image/png", "image/gif", or "image/webp".
func TestProperty_MIMETypeValidationExact(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.String().Draw(rt, "mimeType")
		err := ImageSource{Data: []byte{0xFF}, MIMEType: s}.Validate()

		isValid := validMIMETypes[s]
		if isValid && err != nil {
			rt.Fatalf("Validate() returned error for valid MIME type %q: %v", s, err)
		}
		if !isValid && err == nil {
			rt.Fatalf("Validate() returned nil for invalid MIME type %q", s)
		}
	})
}

// TestProperty_InvalidMIMETypeErrorContainsValue verifies that for any string s
// that is not a valid MIME type, the error returned by Validate() contains s as
// a substring.
func TestProperty_InvalidMIMETypeErrorContainsValue(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.String().Filter(func(s string) bool {
			return !validMIMETypes[s]
		}).Draw(rt, "invalidMIMEType")

		err := ImageSource{Data: []byte{0xFF}, MIMEType: s}.Validate()
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
	data := rapid.SliceOfN(rapid.Byte(), 1, 100).Draw(t, name+"_data")
	return ImageBlock{Source: ImageSource{MIMEType: mime, Data: data}}
}

// TestProperty_LoopPrependsImagesToFirstUserMessage verifies that for any non-empty
// []ImageBlock slice attached via WithImages, after the agent loop builds the first
// user Message, the Content slice starts with those ImageBlock values in the same
// order, followed by the TextBlock.
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
		cp := newCapturingProvider(&ModelResponse{Text: "ok"})
		a, err := New(cp, "sys")
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Invoke with images attached to the context.
		ctx := Background().WithConversationID("conv-pbt").WithImages(images)
		_, invokeErr := a.Invoke(ctx, msg)
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

// TestProperty_ImageBytesRoundTripThroughBase64 verifies that for any byte slice b,
// base64-encoding b to produce a string s, then base64-decoding s, produces a byte
// slice equal to b.
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

// TestProperty_Base64StringRoundTrip verifies that for any valid base64 string s
// (produced by encoding some byte slice), decoding s to bytes and re-encoding to
// base64 produces a string equal to s.
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

// TestProperty_LoopWithImagesPersistsImageBlocksInMemory verifies that for any
// non-empty []ImageBlock slice attached via WithImages, when the agent loop
// completes successfully with memory enabled, the saved conversation history
// contains a user message whose Content slice includes those ImageBlock values.
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
		sp := newScriptedProvider(&ModelResponse{Text: "ok"})

		a, err := New(sp, "sys", WithConversationStore(store))
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Invoke with images attached to the context.
		ctx := Background().WithConversationID("conv-pbt").WithImages(images)
		_, invokeErr := a.Invoke(ctx, "hello")
		if invokeErr != nil {
			rt.Fatalf("unexpected error: %v", invokeErr)
		}

		// Load the saved messages from the store.
		saved, loadErr := testLoadMessages(context.Background(), store, "conv-pbt")
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

// TestProperty_WidgetBlockValidateRejectsEmptyType verifies that Validate()
// returns non-nil for any WidgetBlock with an empty Type, and nil for any
// non-empty Type.
func TestProperty_WidgetBlockValidateRejectsEmptyType(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an arbitrary payload (nil or some JSON bytes).
		useNilPayload := rapid.Bool().Draw(t, "useNilPayload")
		var payload json.RawMessage
		if !useNilPayload {
			raw := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "payloadBytes")
			payload = json.RawMessage(raw)
		}

		// Property 1a: empty Type must be rejected.
		emptyBlock := WidgetBlock{Type: "", Payload: payload}
		if err := emptyBlock.Validate(); err == nil {
			t.Fatalf("Validate() returned nil for WidgetBlock with empty Type; want non-nil error")
		}

		// Property 1b: any non-empty Type must be accepted.
		nonEmptyType := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`).Draw(t, "widgetType")
		validBlock := WidgetBlock{Type: nonEmptyType, Payload: payload}
		if err := validBlock.Validate(); err != nil {
			t.Fatalf("Validate() returned non-nil error %v for WidgetBlock with non-empty Type %q; want nil",
				err, nonEmptyType)
		}
	})
}

func TestDocumentSourceValidateProviderReferences(t *testing.T) {
	tests := []struct {
		name    string
		source  DocumentSource
		wantErr string
	}{
		{
			name:   "provider file ID",
			source: DocumentSource{FileID: "file_abc123"},
		},
		{
			name:   "Bedrock S3 URI with bucket owner",
			source: DocumentSource{S3URI: "s3://example-bucket/reports/quarterly.pdf", S3BucketOwner: "123456789012"},
		},
		{
			name:    "missing source",
			source:  DocumentSource{S3BucketOwner: "123456789012"},
			wantErr: "document source: one of Data, Base64, URL, FileID, or S3URI must be set",
		},
		{
			name:    "multiple provider sources",
			source:  DocumentSource{FileID: "file_abc123", S3URI: "s3://example-bucket/report.pdf"},
			wantErr: "document source: only one of Data, Base64, URL, FileID, or S3URI may be set",
		},
		{
			name:    "invalid S3 scheme",
			source:  DocumentSource{S3URI: "https://example-bucket.s3.amazonaws.com/report.pdf"},
			wantErr: "document source: S3URI must start with s3://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.source.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() returned unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// toolCallContext builds a tool-call child Context outside the engine so
// EmitWidget can be exercised in isolation.
func toolCallContext(id string) (*Context, *toolCallRuntime) {
	rt := &toolCallRuntime{id: id, name: "t"}
	return Background().forInvocation(context.Background(), &invocationRuntime{}).forToolCall(rt), rt
}

// TestEmitWidget_NilPayload verifies that EmitWidget with a nil Payload
// succeeds and stores a WidgetBlock with nil Payload on the call.
func TestEmitWidget_NilPayload(t *testing.T) {
	c, rt := toolCallContext("c1")
	if err := EmitWidget(c, WidgetBlock{Type: "chart"}); err != nil {
		t.Fatalf("EmitWidget with nil Payload returned unexpected error: %v", err)
	}
	drained := rt.drainWidgets()
	if len(drained) != 1 || drained[0].Type != "chart" || drained[0].Payload != nil {
		t.Fatalf("drained = %#v", drained)
	}
}

// TestEmitWidget_WithoutStream_UpdatesCall verifies that without a stream
// consumer EmitWidget still records the block on the tool call.
func TestEmitWidget_WithoutStream_UpdatesCall(t *testing.T) {
	c, rt := toolCallContext("c1")
	if err := EmitWidget(c, WidgetBlock{Type: "progress", Payload: json.RawMessage(`{"value":42}`)}); err != nil {
		t.Fatal(err)
	}
	drained := rt.drainWidgets()
	if len(drained) != 1 || string(drained[0].Payload) != `{"value":42}` {
		t.Fatalf("drained = %#v", drained)
	}
}

// TestEmitWidget_InvalidBlock verifies that an empty Type is rejected and
// nothing is recorded.
func TestEmitWidget_InvalidBlock(t *testing.T) {
	c, rt := toolCallContext("c1")
	if err := EmitWidget(c, WidgetBlock{}); err == nil {
		t.Fatal("expected validation error")
	}
	if len(rt.drainWidgets()) != 0 {
		t.Fatal("invalid block must not be recorded")
	}
}

// TestEmitWidget_ThroughDerivedContext verifies that FromContext finds the
// tool-call Context through stdlib-derived contexts (e.g. middleware that
// wraps ctx with context.WithValue).
func TestEmitWidget_ThroughDerivedContext(t *testing.T) {
	type k struct{}
	c, rt := toolCallContext("c1")
	derived := context.WithValue(c, k{}, "v")
	if err := EmitWidget(derived, WidgetBlock{Type: "table"}); err != nil {
		t.Fatal(err)
	}
	if len(rt.drainWidgets()) != 1 {
		t.Fatal("widget not recorded through derived context")
	}
}

// ---------------------------------------------------------------------------
// Shared generators (reuse widgetTypeGen from event_stream_pbt_test.go is not
// possible across files, so we define local helpers here).
// ---------------------------------------------------------------------------

// loopWidgetTypeGen generates a non-empty widget type string for loop PBT tests.
var loopWidgetTypeGen = rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`)

// loopValidWidgetBlockGen generates a WidgetBlock with a non-empty Type.
func loopValidWidgetBlockGen(t *rapid.T) WidgetBlock {
	useNilPayload := rapid.Bool().Draw(t, "useNilPayload")
	var payload json.RawMessage
	if !useNilPayload {
		raw := rapid.SliceOfN(rapid.Byte(), 1, 32).Draw(t, "payloadBytes")
		payload = json.RawMessage(raw)
	}
	return WidgetBlock{
		Type:    loopWidgetTypeGen.Draw(t, "widgetType"),
		Payload: payload,
	}
}

// ---------------------------------------------------------------------------
// Mock conversation that records Save calls.
// ---------------------------------------------------------------------------

// recordingConversation records every Save call so tests can inspect what was
// persisted. It satisfies the Conversation interface.
type recordingConversation struct {
	mu      sync.Mutex
	saved   [][]Message // one entry per Save call
	history map[string][]Message
}

func newRecordingConversation() *recordingConversation {
	return &recordingConversation{history: make(map[string][]Message)}
}

func (r *recordingConversation) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	msgs := r.history[id]
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	return ConversationSnapshot{Messages: cp}, nil
}

func (r *recordingConversation) Save(_ context.Context, id string, msgs []Message, expectedRevision uint64) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	r.saved = append(r.saved, cp)
	r.history[id] = cp
	return expectedRevision + 1, nil
}

func (r *recordingConversation) List(_ context.Context) ([]string, error) { return nil, nil }
func (r *recordingConversation) Delete(_ context.Context, _ string) error { return nil }

// lastSaved returns the most recently saved message slice, or nil.
func (r *recordingConversation) lastSaved() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.saved) == 0 {
		return nil
	}
	return r.saved[len(r.saved)-1]
}

// ---------------------------------------------------------------------------
// Property 2: EventWidget ordering
//
// For any tool handler that calls EmitWidget, the EventWidget event must
// appear before EventToolEnd for the same tool call in the
// Stream sequence.
// ---------------------------------------------------------------------------

// TestProperty_Loop_EventWidgetOrdering verifies Property 2.
func TestProperty_Loop_EventWidgetOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		block := loopValidWidgetBlockGen(t)

		// Provider: first call returns a single tool call; second call returns
		// a final text answer so the loop terminates.
		sp := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{
				{ToolUseID: "tc-order-1", Name: "widget_tool", Input: json.RawMessage(`{}`)},
			}},
			&ModelResponse{Text: "done"},
		)

		// Tool handler emits the widget then returns.
		widgetTool := newTestRaw(
			"widget_tool",
			"emits a widget",
			map[string]any{"type": "object"},
			func(ctx context.Context, _ json.RawMessage) (string, error) {
				_ = EmitWidget(ctx, block)
				return "result", nil
			},
		)

		a, err := New(sp, "sys", WithTools(widgetTool))
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		events, _ := collectStream(a.Stream(Background(), "go"))

		// Find the index of EventWidget and EventToolEnd for our tool call.
		widgetIdx := -1
		toolEndIdx := -1
		for i, e := range events {
			if e.Type == EventWidget && widgetIdx == -1 {
				widgetIdx = i
			}
			if e.Type == EventToolEnd && e.Tool.Name == "widget_tool" {
				toolEndIdx = i
			}
		}

		if widgetIdx == -1 {
			t.Fatalf("no EventWidget event found in stream; events: %v", eventTypes(events))
		}
		if toolEndIdx == -1 {
			t.Fatalf("no EventToolEnd event found for widget_tool; events: %v", eventTypes(events))
		}
		if widgetIdx >= toolEndIdx {
			t.Fatalf("EventWidget (index %d) must appear before EventToolEnd (index %d); events: %v",
				widgetIdx, toolEndIdx, eventTypes(events))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: No spurious EventWidget events
//
// For any invocation where no handler calls EmitWidget, the channel must
// contain zero events with Type == EventWidget.
// ---------------------------------------------------------------------------

// TestProperty_Loop_NoSpuriousWidgetEvents verifies Property 4.
func TestProperty_Loop_NoSpuriousWidgetEvents(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Provider: first call returns a tool call; second returns final text.
		sp := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{
				{ToolUseID: "tc-nospurious-1", Name: "plain_tool", Input: json.RawMessage(`{}`)},
			}},
			&ModelResponse{Text: "done"},
		)

		// Tool handler does NOT call EmitWidget.
		plainTool := newTestRaw(
			"plain_tool",
			"does not emit widgets",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return "plain result", nil
			},
		)

		a, err := New(sp, "sys", WithTools(plainTool))
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		events, _ := collectStream(a.Stream(Background(), "go"))

		for _, e := range events {
			if e.Type == EventWidget {
				t.Fatalf("unexpected EventWidget event found in stream; events: %v", eventTypes(events))
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Widget persistence in conversation
//
// For any valid WidgetBlock emitted during an invocation with a mock
// Conversation, the saved Message.Content must contain that WidgetBlock.
// ---------------------------------------------------------------------------

// TestProperty_Loop_WidgetPersistenceInConversation verifies Property 5.
func TestProperty_Loop_WidgetPersistenceInConversation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		block := loopValidWidgetBlockGen(t)

		// Provider: first call returns a tool call; second returns final text.
		sp := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{
				{ToolUseID: "tc-persist-1", Name: "persist_tool", Input: json.RawMessage(`{}`)},
			}},
			&ModelResponse{Text: "done"},
		)

		// Tool handler emits the widget.
		persistTool := newTestRaw(
			"persist_tool",
			"emits a widget for persistence test",
			map[string]any{"type": "object"},
			func(ctx context.Context, _ json.RawMessage) (string, error) {
				_ = EmitWidget(ctx, block)
				return "persisted", nil
			},
		)

		conv := newRecordingConversation()
		a, err := New(sp, "sys", WithTools(persistTool),
			WithConversationStore(conv),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, invokeErr := a.Invoke(Background().WithConversationID("conv-persist-1"), "go")
		if invokeErr != nil {
			t.Fatalf("Invoke: %v", invokeErr)
		}

		saved := conv.lastSaved()
		if saved == nil {
			t.Fatalf("conversation.Save was never called")
		}

		// Search all saved messages for the emitted WidgetBlock.
		found := false
		for _, msg := range saved {
			for _, cb := range msg.Content {
				if wb, ok := cb.(WidgetBlock); ok {
					if wb.Type == block.Type {
						found = true
						break
					}
				}
			}
			if found {
				break
			}
		}

		if !found {
			t.Fatalf("WidgetBlock{Type:%q} not found in saved messages; saved: %v",
				block.Type, savedSummary(saved))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 10: Concurrent EmitWidget is race-free
//
// Concurrent goroutines calling EmitWidget simultaneously must produce no
// data races. Run with -race to detect races.
// ---------------------------------------------------------------------------

// TestProperty_Loop_ConcurrentEmitWidgetIsRaceFree verifies Property 10.
func TestProperty_Loop_ConcurrentEmitWidgetIsRaceFree(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate N goroutines (2–8) each emitting a widget.
		n := rapid.IntRange(2, 8).Draw(t, "goroutineCount")

		// Build N distinct widget blocks.
		blocks := make([]WidgetBlock, n)
		for i := range n {
			blocks[i] = loopValidWidgetBlockGen(t)
		}

		// Create an isolated tool-call Context.
		c, acc := toolCallContext("tc-concurrent")

		// Spawn N goroutines all calling EmitWidget concurrently.
		var wg sync.WaitGroup
		wg.Add(n)
		for i := range n {
			go func(idx int) {
				defer wg.Done()
				_ = EmitWidget(c, blocks[idx])
			}(i)
		}
		wg.Wait()

		// Drain the accumulator and verify all N blocks were stored.
		drained := acc.drainWidgets()
		if len(drained) != n {
			t.Fatalf("expected %d blocks in accumulator after concurrent EmitWidget, got %d", n, len(drained))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 11: Provider Message Slice contains no WidgetBlocks
//
// For any []Message slice with arbitrary WidgetBlock distributions, stripWidgets
// must return a slice where no Message.Content contains a WidgetBlock and no
// widget-only messages are included.
// ---------------------------------------------------------------------------

// loopMixedContentGen generates a single ContentBlock of a random type
// (TextBlock, WidgetBlock, ToolUseBlock, or ToolResultBlock).
func loopMixedContentGen(t *rapid.T, label string) ContentBlock {
	blockType := rapid.IntRange(0, 3).Draw(t, label+"_blockType")
	switch blockType {
	case 0:
		text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, label+"_text")
		return TextBlock{Text: text}
	case 1:
		widgetType := rapid.StringMatching(`[a-z]{1,20}`).Draw(t, label+"_widgetType")
		var payload json.RawMessage
		if rapid.Bool().Draw(t, label+"_hasPayload") {
			payload = json.RawMessage(`{"key":"value"}`)
		}
		return WidgetBlock{Type: widgetType, Payload: payload}
	case 2:
		return ToolUseBlock{
			ToolUseID: rapid.StringMatching(`[a-z0-9]{4,12}`).Draw(t, label+"_toolUseID"),
			Name:      rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, label+"_toolName"),
			Input:     json.RawMessage(`{}`),
		}
	default:
		return ToolResultBlock{
			ToolUseID: rapid.StringMatching(`[a-z0-9]{4,12}`).Draw(t, label+"_toolResultID"),
			Content:   rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, label+"_toolResult"),
		}
	}
}

// loopGenerateMixedMessages generates a slice of Messages with arbitrary mixes
// of content block types, including WidgetBlocks at random positions.
func loopGenerateMixedMessages(t *rapid.T) []Message {
	numMessages := rapid.IntRange(0, 10).Draw(t, "numMessages")
	msgs := make([]Message, numMessages)

	for i := range numMessages {
		role := RoleUser
		if rapid.Bool().Draw(t, "isAssistant") {
			role = RoleAssistant
		}

		numBlocks := rapid.IntRange(1, 5).Draw(t, "numBlocks")
		content := make([]ContentBlock, numBlocks)
		for j := range numBlocks {
			label := "m" + string(rune('0'+i%10)) + "b" + string(rune('0'+j%10))
			content[j] = loopMixedContentGen(t, label)
		}

		msgs[i] = Message{Role: role, Content: content}
	}

	return msgs
}

// TestProperty_StripWidgetsNoWidgetBlocks verifies that stripWidgets returns
// a slice where no Message.Content contains a WidgetBlock and no widget-only
// messages are included.
func TestProperty_StripWidgetsNoWidgetBlocks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := loopGenerateMixedMessages(t)

		// Snapshot original widget counts per message to verify no mutation.
		originalWidgetCounts := make([]int, len(msgs))
		for i, m := range msgs {
			for _, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					originalWidgetCounts[i]++
				}
			}
		}

		result := stripWidgets(msgs)

		// No WidgetBlock in any result message.
		for i, m := range result {
			for j, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					t.Fatalf("stripWidgets returned a WidgetBlock at result[%d].Content[%d]", i, j)
				}
			}
		}

		// No empty-content messages in result.
		for i, m := range result {
			if len(m.Content) == 0 {
				t.Fatalf("stripWidgets returned a message with empty Content at result[%d]", i)
			}
		}

		// Input not mutated — original messages still have their WidgetBlocks.
		for i, m := range msgs {
			actualWidgets := 0
			for _, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					actualWidgets++
				}
			}
			if actualWidgets != originalWidgetCounts[i] {
				t.Fatalf("stripWidgets mutated input: msgs[%d] had %d widgets before, has %d after",
					i, originalWidgetCounts[i], actualWidgets)
			}
		}

		// Assert 4: result length <= input length (we only remove, never add).
		if len(result) > len(msgs) {
			t.Fatalf("stripWidgets returned more messages (%d) than input (%d)", len(result), len(msgs))
		}

		// Assert 5: all widget-free messages (those with at least one non-widget block
		// and no widget blocks) must appear in the result.
		noWidgetMsgCount := 0
		for _, m := range msgs {
			hasWidget := false
			hasNonWidget := false
			for _, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					hasWidget = true
				} else {
					hasNonWidget = true
				}
			}
			if !hasWidget && hasNonWidget {
				noWidgetMsgCount++
			}
		}
		if len(result) < noWidgetMsgCount {
			t.Fatalf("stripWidgets dropped widget-free messages: expected at least %d in result, got %d",
				noWidgetMsgCount, len(result))
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// savedSummary returns a compact string representation of saved messages for
// diagnostic output.
func savedSummary(msgs []Message) string {
	var parts []string
	for _, m := range msgs {
		for _, cb := range m.Content {
			switch b := cb.(type) {
			case WidgetBlock:
				parts = append(parts, "WidgetBlock{Type:"+b.Type+"}")
			case TextBlock:
				parts = append(parts, "TextBlock{Text:"+b.Text+"}")
			case ToolUseBlock:
				parts = append(parts, "ToolUseBlock{Name:"+b.Name+"}")
			case ToolResultBlock:
				parts = append(parts, "ToolResultBlock{ID:"+b.ToolUseID+"}")
			default:
				parts = append(parts, "UnknownBlock")
			}
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
