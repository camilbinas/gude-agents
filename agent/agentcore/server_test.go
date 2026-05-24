package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

func newServerTestAgent(t *testing.T, response string) *agent.Agent {
	t.Helper()
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: response}))
	a, err := agent.New(prov, prompt.Text("server test"), nil, agent.WithName("server-test"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a
}

func TestServer_NilAgentReturnsError(t *testing.T) {
	if _, err := NewServer(nil); err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestServer_DefaultsAndOptions(t *testing.T) {
	a := newServerTestAgent(t, "hi")
	s, err := NewServer(a)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if got, want := s.Addr(), ":8080"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}

	s2, err := NewServer(a, WithAddr(":9090"), WithMaxBody(2048), WithDrainTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("NewServer with options: %v", err)
	}
	if got, want := s2.Addr(), ":9090"; got != want {
		t.Errorf("Addr() with WithAddr = %q, want %q", got, want)
	}
}

func TestServer_PingHealthy(t *testing.T) {
	a := newServerTestAgent(t, "ok")
	s, err := NewServer(a)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp pingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != StatusHealthy {
		t.Errorf("status = %q, want %q", resp.Status, StatusHealthy)
	}
	if resp.TimeOfLastUpdate <= 0 {
		t.Errorf("time_of_last_update = %d, want > 0", resp.TimeOfLastUpdate)
	}
}

func TestServer_PingHealthyBusy(t *testing.T) {
	a := newServerTestAgent(t, "ok")
	s, err := NewServer(a)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	s.SetBusy(true)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	var resp pingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != StatusHealthyBusy {
		t.Errorf("status = %q, want %q", resp.Status, StatusHealthyBusy)
	}
}

func TestServer_PingRejectsNonGET(t *testing.T) {
	a := newServerTestAgent(t, "ok")
	s, _ := NewServer(a)

	req := httptest.NewRequest(http.MethodPost, "/ping", nil)
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestServer_InvocationsReturnsAgentResponse(t *testing.T) {
	a := newServerTestAgent(t, "agent says hi")
	s, _ := NewServer(a)

	body, _ := json.Marshal(invocationRequest{Prompt: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp invocationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Response != "agent says hi" {
		t.Errorf("response = %q, want %q", resp.Response, "agent says hi")
	}
	if resp.Status != "success" {
		t.Errorf("status = %q, want success", resp.Status)
	}
}

func TestServer_InvocationsRejectsEmptyPrompt(t *testing.T) {
	a := newServerTestAgent(t, "x")
	s, _ := NewServer(a)

	body, _ := json.Marshal(invocationRequest{Prompt: ""})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestServer_InvocationsRejectsBadJSON(t *testing.T) {
	a := newServerTestAgent(t, "x")
	s, _ := NewServer(a)

	req := httptest.NewRequest(http.MethodPost, "/invocations", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestServer_InvocationsRejectsNonPOST(t *testing.T) {
	a := newServerTestAgent(t, "x")
	s, _ := NewServer(a)

	req := httptest.NewRequest(http.MethodGet, "/invocations", nil)
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestServer_InvocationsReadsSessionHeader(t *testing.T) {
	// Use a conversation store so the conversation ID is observable via
	// the saved messages.
	conv := &capturingConversation{}
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "answer"}))
	a, err := agent.New(prov, prompt.Text("p"), nil,
		agent.WithName("session-test"),
		agent.WithSharedConversation(conv),
	)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	s, _ := NewServer(a)

	body, _ := json.Marshal(invocationRequest{Prompt: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	req.Header.Set(SessionHeader, "session-from-header")
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := conv.lastID(); got != "session-from-header" {
		t.Errorf("conversation id = %q, want %q", got, "session-from-header")
	}
}

func TestServer_InvocationsBodySessionFallback(t *testing.T) {
	conv := &capturingConversation{}
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "answer"}))
	a, err := agent.New(prov, prompt.Text("p"), nil,
		agent.WithName("body-session-test"),
		agent.WithSharedConversation(conv),
	)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	s, _ := NewServer(a)

	body, _ := json.Marshal(invocationRequest{Prompt: "hi", SessionID: "session-from-body"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := conv.lastID(); got != "session-from-body" {
		t.Errorf("conversation id = %q, want %q", got, "session-from-body")
	}
}

func TestServer_InvocationsStreamingSSE(t *testing.T) {
	a := newServerTestAgent(t, "one two three")
	s, _ := NewServer(a)

	body, _ := json.Marshal(invocationRequest{Prompt: "go"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", got)
	}
	body2, _ := io.ReadAll(w.Body)
	if !bytes.Contains(body2, []byte("data:")) {
		t.Errorf("expected SSE data lines, got: %s", body2)
	}
	if !bytes.Contains(body2, []byte("event: done")) {
		t.Errorf("expected done sentinel in SSE stream, got: %s", body2)
	}
}

func TestServer_ListenAndServeStartsAndShutsDown(t *testing.T) {
	a := newServerTestAgent(t, "ok")
	s, err := NewServer(a, WithAddr("127.0.0.1:0"), WithDrainTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Bind a listener manually to discover an unused port, then close it
	// before handing control to the server. This avoids flaky port
	// collisions while still testing the lifecycle.
	// We run with a very short context to validate graceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := s.ListenAndServe(ctx); err != nil {
		t.Errorf("ListenAndServe: %v", err)
	}
}

// --- Test fixtures ---

// capturingConversation records every Save call so tests can assert the
// conversation id flowing through the server. It is the simplest possible
// conversation store implementation that satisfies agent.Conversation.
type capturingConversation struct {
	calls atomic.Pointer[[]string]
}

func (c *capturingConversation) Save(ctx context.Context, id string, msgs []agent.Message) error {
	for {
		old := c.calls.Load()
		var next []string
		if old != nil {
			next = append(next, *old...)
		}
		next = append(next, id)
		if c.calls.CompareAndSwap(old, &next) {
			return nil
		}
	}
}

func (c *capturingConversation) Load(ctx context.Context, id string) ([]agent.Message, error) {
	return []agent.Message{}, nil
}

func (c *capturingConversation) List(ctx context.Context) ([]string, error) {
	p := c.calls.Load()
	if p == nil {
		return nil, nil
	}
	return *p, nil
}

func (c *capturingConversation) Delete(ctx context.Context, id string) error { return nil }

func (c *capturingConversation) lastID() string {
	p := c.calls.Load()
	if p == nil || len(*p) == 0 {
		return ""
	}
	all := *p
	return all[len(all)-1]
}

// --- A/B testing via baggage + bundle client ---

// promptCapturingProvider implements agent.Provider and records the System
// field of every Converse / ConverseStream call.
type promptCapturingProvider struct {
	captured atomic.Pointer[[]string]
}

func (p *promptCapturingProvider) Name() string { return "ab-capture" }

func (p *promptCapturingProvider) Converse(_ context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	p.append(params.System)
	return &agent.ProviderResponse{Text: "ok"}, nil
}

func (p *promptCapturingProvider) ConverseStream(_ context.Context, params agent.ConverseParams, _ agent.StreamCallback) (*agent.ProviderResponse, error) {
	p.append(params.System)
	return &agent.ProviderResponse{Text: "ok"}, nil
}

func (p *promptCapturingProvider) append(s string) {
	for {
		old := p.captured.Load()
		var next []string
		if old != nil {
			next = append(next, *old...)
		}
		next = append(next, s)
		if p.captured.CompareAndSwap(old, &next) {
			return
		}
	}
}

func (p *promptCapturingProvider) all() []string {
	v := p.captured.Load()
	if v == nil {
		return nil
	}
	return *v
}

func TestServer_AppliesBundlePromptFromBaggage(t *testing.T) {
	prov := &promptCapturingProvider{}
	a, err := agent.New(prov, prompt.Text("default prompt"), nil, agent.WithName("ab-test"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	stub := &stubBundleClient{
		resp: newStubResponse(t, "comp-runtime", map[string]any{"system_prompt": "VARIANT-A prompt"}),
	}
	bc, err := NewBundleClient("comp-runtime", withBundleClient(stub))
	if err != nil {
		t.Fatalf("NewBundleClient: %v", err)
	}

	s, err := NewServer(a, WithBundleClient(bc))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	body, _ := json.Marshal(invocationRequest{Prompt: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	req.Header.Set("baggage", "aws.agentcore.configbundle_arn=arn:aws:bedrock-agentcore::123:configuration-bundle/b1,aws.agentcore.configbundle_version=v1")
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	captured := prov.all()
	if len(captured) == 0 || captured[len(captured)-1] != "VARIANT-A prompt" {
		t.Errorf("provider received System=%v, want last = %q", captured, "VARIANT-A prompt")
	}
}

func TestServer_DifferentBundleVersionsRouteToDifferentPrompts(t *testing.T) {
	prov := &promptCapturingProvider{}
	a, _ := agent.New(prov, prompt.Text("default"), nil, agent.WithName("ab-test"))

	// Stub returns different content based on which version was requested.
	stubV1 := &stubBundleClient{
		resp: newStubResponse(t, "comp", map[string]any{"system_prompt": "FORMAL"}),
	}
	stubV2 := &stubBundleClient{
		resp: newStubResponse(t, "comp", map[string]any{"system_prompt": "CASUAL"}),
	}

	bcA, _ := NewBundleClient("comp", withBundleClient(stubV1))
	bcB, _ := NewBundleClient("comp", withBundleClient(stubV2))

	makeRequest := func(s *Server, version string) {
		body, _ := json.Marshal(invocationRequest{Prompt: "hi"})
		req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
		req.Header.Set("baggage", "aws.agentcore.configbundle_arn=arn:::bundle/b,aws.agentcore.configbundle_version="+version)
		w := httptest.NewRecorder()
		s.Mux().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
	}

	sA, _ := NewServer(a, WithBundleClient(bcA))
	sB, _ := NewServer(a, WithBundleClient(bcB))
	makeRequest(sA, "v1")
	makeRequest(sB, "v2")

	captured := prov.all()
	if len(captured) != 2 {
		t.Fatalf("captured %d, want 2", len(captured))
	}
	if captured[0] != "FORMAL" {
		t.Errorf("first call System = %q, want FORMAL", captured[0])
	}
	if captured[1] != "CASUAL" {
		t.Errorf("second call System = %q, want CASUAL", captured[1])
	}
}

func TestServer_NoBaggageFallsBackToAgentInstructions(t *testing.T) {
	prov := &promptCapturingProvider{}
	a, _ := agent.New(prov, prompt.Text("agent default"), nil, agent.WithName("ab-test"))

	stub := &stubBundleClient{
		resp: newStubResponse(t, "comp", map[string]any{"system_prompt": "should not be used"}),
	}
	bc, _ := NewBundleClient("comp", withBundleClient(stub))

	s, _ := NewServer(a, WithBundleClient(bc))

	body, _ := json.Marshal(invocationRequest{Prompt: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	captured := prov.all()
	if len(captured) == 0 || captured[len(captured)-1] != "agent default" {
		t.Errorf("expected agent default prompt, got %v", captured)
	}
	if stub.calls.Load() != 0 {
		t.Error("bundle client should not be called when no baggage is present")
	}
}

func TestServer_BundleResolveErrorFallsBackToDefault(t *testing.T) {
	prov := &promptCapturingProvider{}
	a, _ := agent.New(prov, prompt.Text("default"), nil, agent.WithName("ab-test"))

	stub := &stubBundleClient{err: errors.New("forbidden")}
	bc, _ := NewBundleClient("comp", withBundleClient(stub))

	// Silence error log during the test.
	silent := log.New(io.Discard, "", 0)
	s, _ := NewServer(a, WithBundleClient(bc), WithLogger(silent))

	body, _ := json.Marshal(invocationRequest{Prompt: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	req.Header.Set("baggage", "aws.agentcore.configbundle_arn=arn:::bundle/b,aws.agentcore.configbundle_version=v1")
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (resolution failure should not break the request)", w.Code)
	}
	captured := prov.all()
	if len(captured) == 0 || captured[len(captured)-1] != "default" {
		t.Errorf("expected fallback to agent default, got %v", captured)
	}
}

func TestServer_CustomBundleApplierIsCalled(t *testing.T) {
	prov := &promptCapturingProvider{}
	a, _ := agent.New(prov, prompt.Text("default"), nil, agent.WithName("ab-test"))

	stub := &stubBundleClient{
		resp: newStubResponse(t, "comp", map[string]any{"my_key": "MY-VALUE"}),
	}
	bc, _ := NewBundleClient("comp", withBundleClient(stub))

	customApplier := func(ctx *agent.Context, _ BundleRef, cfg BundleConfig) {
		if v := cfg.String("my_key"); v != "" {
			ctx.WithSystemPromptOverride("custom:" + v)
		}
	}
	s, _ := NewServer(a, WithBundleClient(bc), WithBundleApplier(customApplier))

	body, _ := json.Marshal(invocationRequest{Prompt: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/invocations", bytes.NewReader(body))
	req.Header.Set("baggage", "aws.agentcore.configbundle_arn=arn:::bundle/b,aws.agentcore.configbundle_version=v1")
	w := httptest.NewRecorder()
	s.Mux().ServeHTTP(w, req)

	captured := prov.all()
	if len(captured) == 0 || captured[len(captured)-1] != "custom:MY-VALUE" {
		t.Errorf("custom applier was not called; captured=%v", captured)
	}
}
