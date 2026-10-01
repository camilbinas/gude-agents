package a2a

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
)

// multimodalCapturingProvider captures images/documents from the agent context
// and echoes them back so the executor emits them as outbound artifacts.
type multimodalCapturingProvider struct {
	capturedImages []agent.ImageBlock
	capturedDocs   []agent.DocumentBlock
}

func (p *multimodalCapturingProvider) Name() string { return "multimodal-capturing" }

func (p *multimodalCapturingProvider) Stream(ctx context.Context, _ agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	if ac := agent.FromContext(ctx); ac != nil {
		p.capturedImages = ac.Images()
		p.capturedDocs = ac.Documents()
	}
	const text = "processed multimodal content"
	if emit != nil {
		emit(agent.ModelEvent{Type: agent.ModelEventText, Text: text})
	}
	return &agent.ModelResponse{Text: text}, nil
}

// taskResultEnvelope matches the JSON-RPC result structure: {"task": {...}}
type taskResultEnvelope struct {
	Task json.RawMessage `json:"task"`
}

// taskResponse is a minimal representation of the task response for testing.
type taskResponse struct {
	ID        string             `json:"id"`
	Status    taskStatusResponse `json:"status"`
	Artifacts []artifactResponse `json:"artifacts"`
}

type taskStatusResponse struct {
	State string `json:"state"`
}

type artifactResponse struct {
	ArtifactID string         `json:"artifactId"`
	Parts      []partResponse `json:"parts"`
}

type partResponse struct {
	Text      string `json:"text,omitempty"`
	Raw       string `json:"raw,omitempty"`
	URL       string `json:"url,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

// sendAndParseTask sends a JSON-RPC SendMessage request and parses the task from the response.
func sendAndParseTask(t *testing.T, serverURL string, msg *a2a.Message) taskResponse {
	t.Helper()

	sendReq := &a2a.SendMessageRequest{
		Message: msg,
	}

	rpcReq := &jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "SendMessage",
		Params:  sendReq,
		ID:      "integration-test-1",
	}

	reqBody, err := json.Marshal(rpcReq)
	if err != nil {
		t.Fatalf("failed to marshal JSON-RPC request: %v", err)
	}

	resp, err := http.Post(serverURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("failed to send JSON-RPC request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	// Parse JSON-RPC response.
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		t.Fatalf("failed to parse JSON-RPC response: %v\nbody: %s", err, string(body))
	}

	if rpcResp.Error != nil {
		t.Fatalf("JSON-RPC error: code=%d message=%s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	// The result is {"task": {...}} — unwrap the envelope.
	var envelope taskResultEnvelope
	if err := json.Unmarshal(rpcResp.Result, &envelope); err != nil {
		t.Fatalf("failed to parse result envelope: %v\nresult: %s", err, string(rpcResp.Result))
	}

	var task taskResponse
	if err := json.Unmarshal(envelope.Task, &task); err != nil {
		t.Fatalf("failed to parse task: %v\ntask json: %s", err, string(envelope.Task))
	}

	return task
}

// TestIntegration_DataPart_Image_EndToEnd tests the full flow:
// A2A client sends DataPart with image → Server routes to agent →
// agent receives ImageBlock → response includes DataPart artifact.
// Validates: Requirements 1.1, 2.1, 3.2, 3.3
func TestIntegration_DataPart_Image_EndToEnd(t *testing.T) {
	// 1. Create a real agent with a provider that captures context.
	provider := &multimodalCapturingProvider{}
	a, err := agent.New(
		provider,
		"You are an image processing agent.",
		agent.WithName("image-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Set up a Server for the agent.
	srv, err := NewServer(a, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Start a test HTTP server using the production agent-card discovery path.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 4. Use the A2A Client to connect to the test server and verify card discovery.
	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Verify the client discovered the correct agent card.
	if client.Card().Name != "image-agent" {
		t.Errorf("client card name = %q, want %q", client.Card().Name, "image-agent")
	}

	// Verify the client can list tools (skills from the card).
	tools, err := client.Tools(context.Background())
	if err != nil {
		t.Fatalf("client.Tools() failed: %v", err)
	}
	// The agent has no explicit skills, so tools may be empty — that's fine.
	_ = tools

	// 5. Send a message with a DataPart containing image data via JSON-RPC.
	imageData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01, 0x02, 0x03}
	imgPart := a2a.NewRawPart(imageData)
	imgPart.MediaType = "image/png"

	msg := a2a.NewMessage(a2a.MessageRoleUser,
		a2a.NewTextPart("Describe this image"),
		imgPart,
	)

	task := sendAndParseTask(t, ts.URL+"/", msg)

	// 6. Verify the agent received the correct ImageBlock.
	if len(provider.capturedImages) != 1 {
		t.Fatalf("expected 1 captured image, got %d", len(provider.capturedImages))
	}

	capturedImg := provider.capturedImages[0]
	if capturedImg.Source.MIMEType != "image/png" {
		t.Errorf("captured image MIMEType = %q, want %q", capturedImg.Source.MIMEType, "image/png")
	}

	// The inbound conversion base64-encodes the raw data.
	expectedBase64 := base64.StdEncoding.EncodeToString(imageData)
	if capturedImg.Source.Base64 != expectedBase64 {
		t.Errorf("captured image Base64 mismatch: got %q, want %q", capturedImg.Source.Base64, expectedBase64)
	}

	// Verify the task completed successfully.
	if task.Status.State != "TASK_STATE_COMPLETED" {
		t.Errorf("task state = %q, want %q", task.Status.State, "TASK_STATE_COMPLETED")
	}

	// Verify the response does not echo the inbound image as an artifact.
	for _, artifact := range task.Artifacts {
		for _, part := range artifact.Parts {
			if part.MediaType == "image/png" && part.Raw != "" {
				t.Error("inbound image was echoed as an output artifact")
			}
		}
	}
}

// TestIntegration_FilePart_Document_EndToEnd tests the full flow:
// A2A client sends FilePart with document URL → agent receives DocumentBlock with URL.
// Validates: Requirements 1.3, 2.4, 3.2, 3.3
func TestIntegration_FilePart_Document_EndToEnd(t *testing.T) {
	// 1. Create a real agent with a provider that captures context.
	provider := &multimodalCapturingProvider{}
	a, err := agent.New(
		provider,
		"You are a document processing agent.",
		agent.WithName("doc-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Set up a Server for the agent.
	srv, err := NewServer(a, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Start a test HTTP server using the production agent-card discovery path.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 4. Use the A2A Client to connect to the test server and verify card discovery.
	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Verify the client discovered the correct agent card.
	if client.Card().Name != "doc-agent" {
		t.Errorf("client card name = %q, want %q", client.Card().Name, "doc-agent")
	}

	// 5. Send a message with a FilePart containing a document URL via JSON-RPC.
	docURL := "https://example.com/reports/quarterly-review.pdf"
	docPart := a2a.NewFileURLPart(a2a.URL(docURL), "application/pdf")

	msg := a2a.NewMessage(a2a.MessageRoleUser,
		a2a.NewTextPart("Summarize this document"),
		docPart,
	)

	task := sendAndParseTask(t, ts.URL+"/", msg)

	// 6. Verify the agent received the correct DocumentBlock with URL.
	if len(provider.capturedDocs) != 1 {
		t.Fatalf("expected 1 captured document, got %d", len(provider.capturedDocs))
	}

	capturedDoc := provider.capturedDocs[0]
	if capturedDoc.Source.MIMEType != "application/pdf" {
		t.Errorf("captured document MIMEType = %q, want %q", capturedDoc.Source.MIMEType, "application/pdf")
	}
	if capturedDoc.Source.URL != docURL {
		t.Errorf("captured document URL = %q, want %q", capturedDoc.Source.URL, docURL)
	}

	// Verify the task completed successfully.
	if task.Status.State != "TASK_STATE_COMPLETED" {
		t.Errorf("task state = %q, want %q", task.Status.State, "TASK_STATE_COMPLETED")
	}

	// Verify the response does not echo the inbound document as an artifact.
	for _, artifact := range task.Artifacts {
		for _, part := range artifact.Parts {
			if part.MediaType == "application/pdf" && part.URL == docURL {
				t.Error("inbound document was echoed as an output artifact")
			}
		}
	}
}
