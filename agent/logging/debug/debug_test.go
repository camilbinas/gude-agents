package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

const escape = "\x1b["

type scripted struct {
	responses []*agent.ModelResponse
	err       error
	mu        sync.Mutex
	next      int
}

func (*scripted) Name() string { return "scripted" }
func (s *scripted) Stream(context.Context, agent.ModelRequest, func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	r := s.responses[s.next]
	s.next++
	return r, nil
}

func newAgent(t *testing.T, p agent.Provider, opts ...Option) *agent.Agent {
	t.Helper()
	echo := tool.NewRaw("echo", "echo", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil })
	a, err := agent.New(p, "sys", agent.WithTools(echo), WithLogging(opts...))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func toolThenAnswer() *scripted {
	return &scripted{responses: []*agent.ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "1", Name: "echo", Input: []byte(`{}`)}}},
		{Text: "done", Usage: agent.TokenUsage{InputTokens: 3, OutputTokens: 2}},
	}}
}

func TestWithLoggingDefaultsToColoredStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = w
	a := newAgent(t, toolThenAnswer()) // WithLogging captures os.Stdout at construction
	os.Stdout = original
	if _, err := a.Invoke(agent.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	out, _ := io.ReadAll(r)
	text := string(out)
	if !strings.Contains(text, "▸ invoke") || !strings.Contains(text, "⚙ echo") {
		t.Fatalf("default output missing the lifecycle trace:\n%s", text)
	}
	if !strings.Contains(text, escape) {
		t.Fatalf("default output must use ANSI color:\n%q", text)
	}
}

func TestWithWriterCapturesTraceInsteadOfStdout(t *testing.T) {
	var buf bytes.Buffer
	a := newAgent(t, toolThenAnswer(), WithWriter(&buf))
	if _, err := a.Invoke(agent.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	for _, want := range []string{"▸ invoke", "◉ iteration 1", "⚡provider", "⚙ echo", "✓ echo", "✓ invoke"} {
		if !strings.Contains(text, want) {
			t.Errorf("captured trace is missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, escape) {
		t.Error("a custom writer keeps colors unless WithColor(false) is set")
	}
}

func TestWithColorFalseEmitsNoEscapeSequences(t *testing.T) {
	// Success path.
	var ok bytes.Buffer
	a := newAgent(t, toolThenAnswer(), WithWriter(&ok), WithColor(false))
	if _, err := a.Invoke(agent.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	// Failure path: error and divider lines are colored when color is on.
	var failed bytes.Buffer
	fa := newAgent(t, &scripted{err: errors.New("boom \x1b[31mred\x1b[0m")}, WithWriter(&failed), WithColor(false))
	if _, err := fa.Invoke(agent.Background(), "hi"); err == nil {
		t.Fatal("expected the provider error")
	}
	for name, buf := range map[string]*bytes.Buffer{"success": &ok, "failure": &failed} {
		if strings.ContainsRune(buf.String(), '\x1b') {
			t.Errorf("%s trace contains an escape character:\n%q", name, buf.String())
		}
	}
	if !strings.Contains(ok.String(), "────") || !strings.Contains(failed.String(), "✗ invoke") || !strings.Contains(failed.String(), "boom red") {
		t.Errorf("plain trace lost its structure:\nok=%s\nfailed=%s", ok.String(), failed.String())
	}
	// The same trace with color on does contain escapes (the option matters).
	var colored bytes.Buffer
	ca := newAgent(t, &scripted{err: errors.New("boom")}, WithWriter(&colored), WithColor(true))
	_, _ = ca.Invoke(agent.Background(), "hi")
	if !strings.Contains(colored.String(), escape) {
		t.Error("WithColor(true) should keep colors")
	}
}

func TestWithWriterNilFailsConstruction(t *testing.T) {
	_, err := agent.New(&scripted{}, "sys", WithLogging(WithWriter(nil)))
	if err == nil || !strings.Contains(err.Error(), "writer must not be nil") {
		t.Fatalf("New error = %v, want the nil writer rejected", err)
	}
}

func TestWritesAreSerialized(t *testing.T) {
	var buf bytes.Buffer
	h := newDebugHook(&buf, false)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.p("%s\n", strings.Repeat("x", 200))
		}()
	}
	wg.Wait()
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line != strings.Repeat("x", 200) {
			t.Fatalf("interleaved write: %q", line)
		}
	}
}
