package a2a

import (
	"context"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// fakeProvider implements agent.Provider for testing.
type fakeProvider struct {
	response string
	err      error
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &agent.ProviderResponse{
		Text: f.response,
	}, nil
}

func (f *fakeProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if cb != nil {
		cb(f.response)
	}
	return &agent.ProviderResponse{
		Text: f.response,
	}, nil
}

func newTestAgent(t *testing.T, response string) *agent.Agent {
	t.Helper()
	a, err := agent.New(
		&fakeProvider{response: response},
		prompt.Text("You are a test agent."),
		nil,
		agent.WithName("test-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newErrorAgent(t *testing.T, providerErr error) *agent.Agent {
	t.Helper()
	a, err := agent.New(
		&fakeProvider{err: providerErr},
		prompt.Text("You are a test agent."),
		nil,
		agent.WithName("test-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestExecutor_Execute_NewTask(t *testing.T) {
	a := newTestAgent(t, "Hello from agent")
	executor := NewExecutor(a, nil)

	execCtx := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hi")),
		TaskID:     a2a.NewTaskID(),
		StoredTask: nil, // new task
	}

	var events []a2a.Event
	for event, err := range executor.Execute(context.Background(), execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	// Expect: submitted task, working status, artifact(s), completed status
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(events))
	}

	// First event should be a submitted Task.
	if _, ok := events[0].(*a2a.Task); !ok {
		t.Errorf("event[0]: expected *a2a.Task, got %T", events[0])
	}

	// Second event should be a working status update.
	if su, ok := events[1].(*a2a.TaskStatusUpdateEvent); ok {
		if su.Status.State != a2a.TaskStateWorking {
			t.Errorf("event[1]: expected working state, got %s", su.Status.State)
		}
	} else {
		t.Errorf("event[1]: expected *a2a.TaskStatusUpdateEvent, got %T", events[1])
	}

	// Last event should be a completed status update.
	last := events[len(events)-1]
	if su, ok := last.(*a2a.TaskStatusUpdateEvent); ok {
		if su.Status.State != a2a.TaskStateCompleted {
			t.Errorf("last event: expected completed state, got %s", su.Status.State)
		}
	} else {
		t.Errorf("last event: expected *a2a.TaskStatusUpdateEvent, got %T", last)
	}
}

func TestExecutor_Execute_ExistingTask(t *testing.T) {
	a := newTestAgent(t, "Continued response")
	executor := NewExecutor(a, nil)

	existingTask := &a2a.Task{
		ID:        a2a.NewTaskID(),
		ContextID: a2a.NewContextID(),
		Status:    a2a.TaskStatus{State: a2a.TaskStateInputRequired},
	}

	execCtx := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Continue")),
		TaskID:     existingTask.ID,
		StoredTask: existingTask,
	}

	var events []a2a.Event
	for event, err := range executor.Execute(context.Background(), execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	// For existing tasks, no submitted event — starts with working.
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	// First event should be working status (no submitted for existing tasks).
	if su, ok := events[0].(*a2a.TaskStatusUpdateEvent); ok {
		if su.Status.State != a2a.TaskStateWorking {
			t.Errorf("event[0]: expected working state, got %s", su.Status.State)
		}
	} else {
		t.Errorf("event[0]: expected *a2a.TaskStatusUpdateEvent, got %T", events[0])
	}
}

func TestExecutor_Execute_Error(t *testing.T) {
	a := newErrorAgent(t, context.DeadlineExceeded)
	executor := NewExecutor(a, nil)

	execCtx := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hi")),
		TaskID:     a2a.NewTaskID(),
		StoredTask: nil,
	}

	var events []a2a.Event
	for event, err := range executor.Execute(context.Background(), execCtx) {
		if err != nil {
			t.Fatalf("unexpected error from iterator: %v", err)
		}
		events = append(events, event)
	}

	// Should end with a failed status.
	last := events[len(events)-1]
	su, ok := last.(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("last event: expected *a2a.TaskStatusUpdateEvent, got %T", last)
	}
	if su.Status.State != a2a.TaskStateFailed {
		t.Errorf("expected failed state, got %s", su.Status.State)
	}
}

func TestExecutor_Cancel(t *testing.T) {
	a := newTestAgent(t, "")
	executor := NewExecutor(a, nil)

	execCtx := &a2asrv.ExecutorContext{
		TaskID: a2a.NewTaskID(),
	}

	var events []a2a.Event
	for event, err := range executor.Cancel(context.Background(), execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	su, ok := events[0].(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("expected *a2a.TaskStatusUpdateEvent, got %T", events[0])
	}
	if su.Status.State != a2a.TaskStateCanceled {
		t.Errorf("expected canceled state, got %s", su.Status.State)
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name string
		msg  *a2a.Message
		want string
	}{
		{
			name: "nil message",
			msg:  nil,
			want: "",
		},
		{
			name: "single text part",
			msg:  a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
			want: "hello",
		},
		{
			name: "multiple text parts",
			msg:  a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"), a2a.NewTextPart("world")),
			want: "hello\nworld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractText(tt.msg)
			if got != tt.want {
				t.Errorf("extractText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Multimodal Executor Tests ---
// These tests verify the enhanced executor's multimodal flow:
// inbound DataPart/FilePart → ImageBlock/DocumentBlock on context,
// and outbound ImageBlock/DocumentBlock → DataPart/FilePart artifact events.

// contextCapturingProvider captures the agent context during ConverseStream
// so tests can inspect what images/documents were attached.
type contextCapturingProvider struct {
	response       string
	capturedImages []agent.ImageBlock
	capturedDocs   []agent.DocumentBlock
}

func (p *contextCapturingProvider) Name() string { return "capturing" }

func (p *contextCapturingProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	if ac := agent.FromContext(ctx); ac != nil {
		p.capturedImages = ac.Images()
		p.capturedDocs = ac.Documents()
	}
	return &agent.ProviderResponse{Text: p.response}, nil
}

func (p *contextCapturingProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	if ac := agent.FromContext(ctx); ac != nil {
		p.capturedImages = ac.Images()
		p.capturedDocs = ac.Documents()
	}
	if cb != nil {
		cb(p.response)
	}
	return &agent.ProviderResponse{Text: p.response}, nil
}

func newCapturingAgent(t *testing.T, provider *contextCapturingProvider) *agent.Agent {
	t.Helper()
	a, err := agent.New(
		provider,
		prompt.Text("You are a test agent."),
		nil,
		agent.WithName("capturing-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// collectEvents runs the executor and collects all emitted events.
func collectEvents(t *testing.T, executor *Executor, execCtx *a2asrv.ExecutorContext) []a2a.Event {
	t.Helper()
	var events []a2a.Event
	for event, err := range executor.Execute(context.Background(), execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}
	return events
}

// TestExecutor_InboundDataPart_ImageMIME verifies that an inbound DataPart with
// an image MIME type is converted to an ImageBlock and attached to the agent context.
// Validates: Requirements 1.1
func TestExecutor_InboundDataPart_ImageMIME(t *testing.T) {
	provider := &contextCapturingProvider{response: "got image"}
	a := newCapturingAgent(t, provider)
	executor := NewExecutor(a, nil)

	// Create a DataPart with image/png MIME type containing raw bytes.
	imageData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	imgPart := a2a.NewRawPart(imageData)
	imgPart.MediaType = "image/png"

	execCtx := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Describe this image"), imgPart),
		TaskID:     a2a.NewTaskID(),
		StoredTask: nil,
	}

	collectEvents(t, executor, execCtx)

	// Verify the provider received an ImageBlock on the context.
	if len(provider.capturedImages) != 1 {
		t.Fatalf("expected 1 captured image, got %d", len(provider.capturedImages))
	}

	img := provider.capturedImages[0]
	if img.Source.MIMEType != "image/png" {
		t.Errorf("expected MIMEType image/png, got %q", img.Source.MIMEType)
	}
	if img.Source.Base64 == "" {
		t.Error("expected Base64 to be populated from DataPart raw content")
	}

	// No documents should be captured.
	if len(provider.capturedDocs) != 0 {
		t.Errorf("expected 0 captured documents, got %d", len(provider.capturedDocs))
	}
}

// TestExecutor_InboundFilePart_DocumentMIME verifies that an inbound FilePart with
// a document MIME type is converted to a DocumentBlock and attached to the agent context.
// Validates: Requirements 1.3
func TestExecutor_InboundFilePart_DocumentMIME(t *testing.T) {
	provider := &contextCapturingProvider{response: "got document"}
	a := newCapturingAgent(t, provider)
	executor := NewExecutor(a, nil)

	// Create a FilePart with application/pdf MIME type and a URL.
	docURL := "https://example.com/report.pdf"
	docPart := a2a.NewFileURLPart(a2a.URL(docURL), "application/pdf")

	execCtx := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Summarize this document"), docPart),
		TaskID:     a2a.NewTaskID(),
		StoredTask: nil,
	}

	collectEvents(t, executor, execCtx)

	// Verify the provider received a DocumentBlock on the context.
	if len(provider.capturedDocs) != 1 {
		t.Fatalf("expected 1 captured document, got %d", len(provider.capturedDocs))
	}

	doc := provider.capturedDocs[0]
	if doc.Source.MIMEType != "application/pdf" {
		t.Errorf("expected MIMEType application/pdf, got %q", doc.Source.MIMEType)
	}
	if doc.Source.URL != docURL {
		t.Errorf("expected URL %q, got %q", docURL, doc.Source.URL)
	}

	// No images should be captured.
	if len(provider.capturedImages) != 0 {
		t.Errorf("expected 0 captured images, got %d", len(provider.capturedImages))
	}
}

// TestExecutor_OutboundImageBlock_Base64_EmitsDataPart verifies that when the agent
// context has an ImageBlock with Base64 data, the executor emits a DataPart artifact
// event with the correct content and media type.
// Validates: Requirements 2.1
func TestExecutor_OutboundImageBlock_Base64_EmitsDataPart(t *testing.T) {
	provider := &contextCapturingProvider{response: "here is the image"}
	a := newCapturingAgent(t, provider)
	executor := NewExecutor(a, nil)

	// Send a message with an image DataPart — the executor will attach it to the
	// context via ConvertInbound, and after streaming it will emit it as an outbound
	// artifact via ConvertOutboundImage.
	imageData := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG header bytes
	imgPart := a2a.NewRawPart(imageData)
	imgPart.MediaType = "image/jpeg"

	execCtx := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, imgPart),
		TaskID:     a2a.NewTaskID(),
		StoredTask: nil,
	}

	events := collectEvents(t, executor, execCtx)

	// Find the artifact event that contains the multimodal part (not the text artifact).
	// The executor emits: submitted, working, text artifact(s), multimodal artifact, completed.
	var foundDataPart bool
	for _, event := range events {
		ae, ok := event.(*a2a.TaskArtifactUpdateEvent)
		if !ok {
			continue
		}
		for _, part := range ae.Artifact.Parts {
			if part.MediaType == "image/jpeg" && part.Raw() != nil {
				foundDataPart = true
				break
			}
		}
	}

	if !foundDataPart {
		t.Error("expected a DataPart artifact event with image/jpeg media type, but none was found")
	}
}

// TestExecutor_OutboundDocumentBlock_URL_EmitsFilePart verifies that when the agent
// context has a DocumentBlock with a URL, the executor emits a FilePart artifact
// event with the correct URI and media type.
// Validates: Requirements 2.4
func TestExecutor_OutboundDocumentBlock_URL_EmitsFilePart(t *testing.T) {
	provider := &contextCapturingProvider{response: "here is the document"}
	a := newCapturingAgent(t, provider)
	executor := NewExecutor(a, nil)

	// Send a message with a document FilePart — the executor will attach it to the
	// context via ConvertInbound, and after streaming it will emit it as an outbound
	// artifact via ConvertOutboundDocument.
	docURL := "https://example.com/analysis.pdf"
	docPart := a2a.NewFileURLPart(a2a.URL(docURL), "application/pdf")

	execCtx := &a2asrv.ExecutorContext{
		Message:    a2a.NewMessage(a2a.MessageRoleUser, docPart),
		TaskID:     a2a.NewTaskID(),
		StoredTask: nil,
	}

	events := collectEvents(t, executor, execCtx)

	// Find the artifact event that contains the FilePart (URL-based part).
	var foundFilePart bool
	for _, event := range events {
		ae, ok := event.(*a2a.TaskArtifactUpdateEvent)
		if !ok {
			continue
		}
		for _, part := range ae.Artifact.Parts {
			if part.MediaType == "application/pdf" && part.URL() == a2a.URL(docURL) {
				foundFilePart = true
				break
			}
		}
	}

	if !foundFilePart {
		t.Error("expected a FilePart artifact event with application/pdf media type and matching URL, but none was found")
	}
}
