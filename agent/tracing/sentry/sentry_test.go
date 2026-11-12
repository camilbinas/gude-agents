package sentry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	gosentry "github.com/getsentry/sentry-go"

	agent "github.com/camilbinas/gude-agents/agent"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		max      int
		want     string
		wantRune bool // result (minus ellipsis) must stay valid UTF-8
	}{
		{name: "short ascii unchanged", in: "hello", max: 200, want: "hello"},
		{name: "exact length unchanged", in: "abcde", max: 5, want: "abcde"},
		{name: "ascii truncated", in: "abcdef", max: 3, want: "abc..."},
		{name: "empty", in: "", max: 10, want: ""},
		{name: "negative max", in: "abc", max: -1, want: "..."},
		{name: "zero max", in: "abc", max: 0, want: "..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateRunes(tc.in, tc.max); got != tc.want {
				t.Fatalf("truncateRunes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// TestTruncateRunesUTF8Safe is the regression test for the byte-slicing bug:
// truncation must land on a rune boundary and always produce valid UTF-8.
func TestTruncateRunesUTF8Safe(t *testing.T) {
	// Each "世" is 3 bytes. A byte-offset slice at an odd boundary would split
	// a rune and emit invalid UTF-8.
	s := strings.Repeat("世", 300) // 900 bytes, 300 runes
	for _, max := range []int{0, 1, 50, 199, 200, 299, 300} {
		got := truncateRunes(s, max)
		trimmed := strings.TrimSuffix(got, "...")
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d: result is not valid UTF-8: %q", max, got)
		}
		if n := utf8.RuneCountInString(trimmed); n > max {
			t.Fatalf("max=%d: kept %d runes, want <= %d", max, n, max)
		}
	}
}

// recordingTransport captures events (which carry breadcrumbs) for assertions.
type recordingTransport struct {
	mu     sync.Mutex
	events []*gosentry.Event
}

func (r *recordingTransport) Configure(gosentry.ClientOptions) {}
func (r *recordingTransport) SendEvent(e *gosentry.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}
func (r *recordingTransport) Flush(time.Duration) bool              { return true }
func (r *recordingTransport) FlushWithContext(context.Context) bool { return true }
func (r *recordingTransport) Close()                                {}

// runBreadcrumbTool runs a tool through BreadcrumbMiddleware on an isolated hub
// and returns the breadcrumb data recorded for the call.
func runBreadcrumbTool(t *testing.T, result agent.ToolResult, toolErr error, opts ...MiddlewareOption) map[string]any {
	t.Helper()

	client, err := gosentry.NewClient(gosentry.ClientOptions{
		Dsn:       "https://test@o0.ingest.sentry.io/0",
		Transport: &recordingTransport{},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// BreadcrumbMiddleware records on the current hub (gosentry.AddBreadcrumb),
	// so rebind the current hub's client/scope for the duration of this call
	// and restore it afterwards.
	cur := gosentry.CurrentHub()
	prevClient := cur.Client()
	cur.BindClient(client)
	cur.PushScope()
	defer func() {
		cur.PopScope()
		cur.BindClient(prevClient)
	}()

	mw := BreadcrumbMiddleware(opts...)
	handler := mw(func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
		return result, toolErr
	})

	if _, err := handler(context.Background(), agent.ToolCall{Name: "search", Input: []byte(`{"q":"secret-query"}`)}); !errors.Is(err, toolErr) {
		t.Fatalf("handler err = %v, want %v", err, toolErr)
	}

	// Trigger an event so breadcrumbs attached to the scope are captured.
	cur.CaptureMessage("probe")

	tr := client.Transport.(*recordingTransport)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.events) == 0 {
		t.Fatal("no events captured")
	}
	ev := tr.events[len(tr.events)-1]
	if len(ev.Breadcrumbs) == 0 {
		t.Fatal("no breadcrumbs recorded on event")
	}
	return ev.Breadcrumbs[len(ev.Breadcrumbs)-1].Data
}

func TestBreadcrumbMiddlewareOmitsContentByDefault(t *testing.T) {
	data := runBreadcrumbTool(t, agent.ToolResult{Text: "sensitive-result"}, nil)

	if _, ok := data["input"]; ok {
		t.Errorf("default breadcrumb leaked tool input: %v", data["input"])
	}
	if _, ok := data["result"]; ok {
		t.Errorf("default breadcrumb leaked tool result: %v", data["result"])
	}
	if data["tool"] != "search" {
		t.Errorf("tool = %v, want search", data["tool"])
	}
	if _, ok := data["duration"]; !ok {
		t.Error("duration not recorded")
	}
}

func TestBreadcrumbMiddlewareWithContent(t *testing.T) {
	data := runBreadcrumbTool(t, agent.ToolResult{Text: "sensitive-result"}, nil, WithBreadcrumbContent())

	if got, _ := data["input"].(string); !strings.Contains(got, "secret-query") {
		t.Errorf("input = %q, want it to contain the query", got)
	}
	if got, _ := data["result"].(string); got != "sensitive-result" {
		t.Errorf("result = %q, want sensitive-result", got)
	}
}

func TestBreadcrumbMiddlewareErrorOmitsResult(t *testing.T) {
	data := runBreadcrumbTool(t, agent.ToolResult{}, errors.New("boom"), WithBreadcrumbContent())

	if data["error"] != "boom" {
		t.Errorf("error = %v, want boom", data["error"])
	}
	if _, ok := data["result"]; ok {
		t.Error("result should not be recorded on error")
	}
}
