package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// probe tracks how many handlers run at once and the order they start in.
type probe struct {
	mu      sync.Mutex
	current int
	max     int
	started []string
}

func (p *probe) enter(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current++
	if p.current > p.max {
		p.max = p.current
	}
	p.started = append(p.started, name)
}

func (p *probe) leave() {
	p.mu.Lock()
	p.current--
	p.mu.Unlock()
}

func (p *probe) snapshot() (max int, started []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max, append([]string(nil), p.started...)
}

// probedTool records concurrency and holds the handler for hold, or until
// release is closed when it is non-nil.
func probedTool(p *probe, name string, hold time.Duration, release <-chan struct{}, opts ...tool.Option) tool.Tool {
	return tool.NewRaw(name, name, nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
		p.enter(name)
		defer p.leave()
		if release != nil {
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
				return "", fmt.Errorf("%s: never released", name)
			}
		} else {
			time.Sleep(hold)
		}
		return name + " result", nil
	}, opts...)
}

func batchOf(names ...string) []tool.Call {
	calls := make([]tool.Call, len(names))
	for i, name := range names {
		calls[i] = tool.Call{ToolUseID: "id-" + name, Name: name, Input: json.RawMessage(`{}`)}
	}
	return calls
}

// resultIDs returns the tool result IDs and contents of the last message sent
// to the provider, in the order the provider will read them.
func resultIDs(t *testing.T, req ModelRequest) (ids, contents []string) {
	t.Helper()
	last := req.Messages[len(req.Messages)-1]
	for _, block := range last.Content {
		result, ok := block.(ToolResultBlock)
		if !ok {
			t.Fatalf("unexpected block %#v in result message", block)
		}
		ids = append(ids, result.ToolUseID)
		contents = append(contents, result.Content)
	}
	return ids, contents
}

func TestToolExecutionDefaultsToUnboundedParallel(t *testing.T) {
	a, err := New(mockProvider{}, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if a.toolExecution != ToolExecutionParallel || a.maxConcurrentTools != 0 || a.concurrentLimit() != 0 {
		t.Fatalf("default policy = %v/%d/%d, want unbounded parallel", a.toolExecution, a.maxConcurrentTools, a.concurrentLimit())
	}
	if ToolExecutionParallel != 0 {
		t.Fatal("ToolExecutionParallel must be the zero value")
	}
}

func TestToolExecutionOptionValidation(t *testing.T) {
	cases := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{"unknown mode", []Option{WithToolExecution(ToolExecutionMode(9))}, "unknown mode 9"},
		{"zero limit", []Option{WithMaxConcurrentTools(0)}, "n must be >= 1"},
		{"negative limit", []Option{WithMaxConcurrentTools(-3)}, "n must be >= 1"},
		{"sequential with limit above one", []Option{WithToolExecution(ToolExecutionSequential), WithMaxConcurrentTools(2)}, "conflicts"},
		{"limit above one then sequential", []Option{WithMaxConcurrentTools(2), WithToolExecution(ToolExecutionSequential)}, "conflicts"},
		{"sequential with limit one", []Option{WithToolExecution(ToolExecutionSequential), WithMaxConcurrentTools(1)}, ""},
		{"parallel with limit", []Option{WithToolExecution(ToolExecutionParallel), WithMaxConcurrentTools(4)}, ""},
		{"limit alone", []Option{WithMaxConcurrentTools(4)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(mockProvider{}, "sys", tc.opts...)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("New error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestToolExecutionModeString(t *testing.T) {
	if ToolExecutionParallel.String() != "parallel" || ToolExecutionSequential.String() != "sequential" {
		t.Fatalf("names = %q, %q", ToolExecutionParallel, ToolExecutionSequential)
	}
	if got := ToolExecutionMode(7).String(); got != "ToolExecutionMode(7)" {
		t.Fatalf("unknown mode name = %q", got)
	}
}

func TestToolExecutionSequentialRunsOneAtATimeInProviderOrder(t *testing.T) {
	p := &probe{}
	names := []string{"a", "b", "c", "d"}
	tools := make([]tool.Tool, len(names))
	for i, name := range names {
		// Earlier tools are slower, so any overlap would reorder the starts.
		tools[i] = probedTool(p, name, time.Duration(len(names)-i)*5*time.Millisecond, nil)
	}
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: batchOf(names...)}, {Text: "done"},
	}}
	a, err := New(provider, "sys", WithTools(tools...), WithToolExecution(ToolExecutionSequential))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	max, started := p.snapshot()
	if max != 1 {
		t.Fatalf("max concurrent handlers = %d, want 1", max)
	}
	if strings.Join(started, ",") != "a,b,c,d" {
		t.Fatalf("start order = %v, want provider order", started)
	}
}

func TestToolExecutionLimitOfOneRunsStrictlyInOrder(t *testing.T) {
	p := &probe{}
	names := []string{"a", "b", "c"}
	tools := make([]tool.Tool, len(names))
	for i, name := range names {
		tools[i] = probedTool(p, name, 5*time.Millisecond, nil)
	}
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: batchOf(names...)}, {Text: "done"},
	}}
	a, err := New(provider, "sys", WithTools(tools...), WithMaxConcurrentTools(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	max, started := p.snapshot()
	if max != 1 || strings.Join(started, ",") != "a,b,c" {
		t.Fatalf("max=%d started=%v, want one at a time in provider order", max, started)
	}
}

func TestToolExecutionBoundsConcurrentHandlers(t *testing.T) {
	const limit = 3
	p := &probe{}
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	release := make(chan struct{})
	tools := make([]tool.Tool, len(names))
	for i, name := range names {
		tools[i] = probedTool(p, name, 0, release)
	}
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: batchOf(names...)}, {Text: "done"},
	}}
	a, err := New(provider, "sys", WithTools(tools...), WithMaxConcurrentTools(limit))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.Invoke(Background(), "go")
		done <- err
	}()
	// Wait until the limit is saturated, then check nothing beyond it started.
	deadline := time.After(5 * time.Second)
	for {
		p.mu.Lock()
		running := p.current
		p.mu.Unlock()
		if running == limit {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d handlers running, want %d", running, limit)
		case <-time.After(time.Millisecond):
		}
	}
	time.Sleep(30 * time.Millisecond)
	if max, _ := p.snapshot(); max != limit {
		t.Fatalf("max concurrent handlers while saturated = %d, want %d", max, limit)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	max, started := p.snapshot()
	if max != limit {
		t.Fatalf("max concurrent handlers = %d, want %d", max, limit)
	}
	if len(started) != len(names) {
		t.Fatalf("started %d handlers, want %d", len(started), len(names))
	}
	// Handlers are admitted in provider order, so the first wave is a, b, c
	// (their relative start order is a scheduling race).
	wave := append([]string(nil), started[:limit]...)
	sort.Strings(wave)
	if strings.Join(wave, ",") != "a,b,c" {
		t.Fatalf("first wave = %v, want a, b, c", started[:limit])
	}
}

func TestToolExecutionKeepsProviderOrderUnderConcurrency(t *testing.T) {
	names := []string{"slow", "medium", "fast", "instant"}
	delays := []time.Duration{60 * time.Millisecond, 40 * time.Millisecond, 20 * time.Millisecond, 0}
	for _, limit := range []int{0, 2} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			p := &probe{}
			tools := make([]tool.Tool, len(names))
			for i, name := range names {
				tools[i] = probedTool(p, name, delays[i], nil)
			}
			provider := &approvalBatchProvider{responses: []*ModelResponse{
				{ToolCalls: batchOf(names...)}, {Text: "done"},
			}}
			opts := []Option{WithTools(tools...)}
			if limit > 0 {
				opts = append(opts, WithMaxConcurrentTools(limit))
			}
			a, err := New(provider, "sys", opts...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Invoke(Background(), "go"); err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			req := provider.params[1]
			provider.mu.Unlock()
			ids, contents := resultIDs(t, req)
			for i, name := range names {
				if ids[i] != "id-"+name || contents[i] != name+" result" {
					t.Fatalf("result %d = %s/%q, want %s in provider order (all: %v)", i, ids[i], contents[i], name, ids)
				}
			}
		})
	}
}

// Under approval, no sibling handler runs before the pause is persisted, and
// the resumed batch still follows the configured execution policy.
func TestToolExecutionPolicyAppliesToApprovalResume(t *testing.T) {
	type policy struct {
		name      string
		opts      []Option
		wantMax   int
		wantOrder string
	}
	for _, tc := range []policy{
		{name: "sequential", opts: []Option{WithToolExecution(ToolExecutionSequential)}, wantMax: 1, wantOrder: "approve,x,y,z"},
		{name: "limit 2", opts: []Option{WithMaxConcurrentTools(2)}, wantMax: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &probe{}
			var gate chan struct{}
			if tc.wantMax > 1 {
				// A limit above one must actually run two handlers together.
				gate = make(chan struct{})
			}
			var releaseOnce sync.Once
			var running atomic.Int32
			mk := func(name string, opts ...tool.Option) tool.Tool {
				return tool.NewRaw(name, name, nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
					p.enter(name)
					defer p.leave()
					if gate != nil {
						if running.Add(1) >= int32(tc.wantMax) {
							releaseOnce.Do(func() { close(gate) })
						}
						select {
						case <-gate:
						case <-time.After(5 * time.Second):
							return "", fmt.Errorf("%s: %d handlers never ran together", name, tc.wantMax)
						}
					} else {
						time.Sleep(5 * time.Millisecond)
					}
					return name + " result", nil
				}, opts...)
			}
			provider := &approvalBatchProvider{responses: []*ModelResponse{
				{ToolCalls: batchOf("approve", "x", "y", "z")}, {Text: "done"},
			}}
			opts := append([]Option{withPauseStores(), WithTools(mk("approve", tool.RequiresApproval()), mk("x"), mk("y"), mk("z"))}, tc.opts...)
			a, err := New(provider, "sys", opts...)
			if err != nil {
				t.Fatal(err)
			}
			in := mustInterrupt(t, a, Background(), "go")
			if _, started := p.snapshot(); len(started) != 0 {
				t.Fatalf("handlers ran before the approval batch was persisted: %v", started)
			}
			if _, err := a.Resume(Background(), in, Approve()); err != nil {
				t.Fatal(err)
			}
			max, started := p.snapshot()
			if max != tc.wantMax {
				t.Fatalf("max concurrent handlers on resume = %d, want %d", max, tc.wantMax)
			}
			if tc.wantOrder != "" && strings.Join(started, ",") != tc.wantOrder {
				t.Fatalf("resume start order = %v, want %s", started, tc.wantOrder)
			}
			provider.mu.Lock()
			req := provider.params[1]
			provider.mu.Unlock()
			ids, _ := resultIDs(t, req)
			if strings.Join(ids, ",") != "id-approve,id-x,id-y,id-z" {
				t.Fatalf("resumed results = %v, want provider order", ids)
			}
		})
	}
}

// A durable run (conversation plus execution store) applies the same policy to
// ordinary tool batches.
func TestToolExecutionPolicyAppliesToDurableRuns(t *testing.T) {
	p := &probe{}
	names := []string{"a", "b", "c"}
	tools := make([]tool.Tool, len(names))
	for i, name := range names {
		tools[i] = probedTool(p, name, 5*time.Millisecond, nil)
	}
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: batchOf(names...)}, {Text: "done"},
	}}
	a, err := New(provider, "sys", withPauseStores(), WithTools(tools...), WithToolExecution(ToolExecutionSequential))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background().WithConversationID("durable-seq"), "go"); err != nil {
		t.Fatal(err)
	}
	max, started := p.snapshot()
	if max != 1 || strings.Join(started, ",") != "a,b,c" {
		t.Fatalf("durable run: max=%d started=%v, want sequential provider order", max, started)
	}
}
