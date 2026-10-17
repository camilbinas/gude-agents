package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeSession stands in for one MCP subprocess session.
type fakeSession struct {
	id      int
	busy    atomic.Bool
	closed  atomic.Bool
	overlap *atomic.Bool // set when two calls ran on this session at once
	// call runs inside CallTool while the session is marked busy.
	call func(ctx context.Context, s *fakeSession) error
}

func (s *fakeSession) CallTool(ctx context.Context, _ *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error) {
	if !s.busy.CompareAndSwap(false, true) {
		s.overlap.Store(true)
		return nil, errors.New("session used by two calls at once")
	}
	defer s.busy.Store(false)
	if s.call != nil {
		if err := s.call(ctx, s); err != nil {
			return nil, err
		}
	}
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}}}, nil
}

func (s *fakeSession) Close() error {
	s.closed.Store(true)
	return nil
}

// fakeServer creates fakeSessions for a Pool and counts them.
type fakeServer struct {
	mu       sync.Mutex
	sessions []*fakeSession
	overlap  atomic.Bool
	// behavior configures the session with the given creation index.
	behavior func(n int) func(ctx context.Context, s *fakeSession) error
	// failConnect makes the n-th (0-based) connect attempt fail.
	failConnect map[int]bool
	attempts    int
}

func (f *fakeServer) connect(context.Context) (poolSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.attempts
	f.attempts++
	if f.failConnect[n] {
		return nil, errors.New("subprocess failed to start")
	}
	s := &fakeSession{id: len(f.sessions), overlap: &f.overlap}
	if f.behavior != nil {
		s.call = f.behavior(s.id)
	}
	f.sessions = append(f.sessions, s)
	return s, nil
}

func (f *fakeServer) created() []*fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeSession(nil), f.sessions...)
}

// newFakePool builds a Pool over fake sessions with an empty idle set.
func newFakePool(maxSize int, server *fakeServer) *Pool {
	p := &Pool{
		maxSize:  maxSize,
		sessions: make(chan poolSession, maxSize),
		connect:  server.connect,
		toolDefs: []*sdkmcp.Tool{{Name: "work", InputSchema: map[string]any{"type": "object"}}},
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func workTool(t *testing.T, p *Pool) tool.Tool {
	t.Helper()
	tools, err := p.Tools()
	if err != nil || len(tools) != 1 {
		t.Fatalf("Tools() = %d tools, err %v", len(tools), err)
	}
	return tools[0]
}

func call(tl tool.Tool, ctx context.Context) error {
	_, err := tl.Handler(ctx, json.RawMessage(`{}`))
	return err
}

func TestPoolNeverLeasesOneSessionToTwoCalls(t *testing.T) {
	server := &fakeServer{behavior: func(int) func(context.Context, *fakeSession) error {
		return func(context.Context, *fakeSession) error { time.Sleep(2 * time.Millisecond); return nil }
	}}
	p := newFakePool(3, server)
	tl := workTool(t, p)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- call(tl, context.Background()) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("call failed: %v", err)
		}
	}
	if server.overlap.Load() {
		t.Fatal("a session served two simultaneous calls")
	}
}

func TestPoolSizeBoundsSessionCreation(t *testing.T) {
	const maxSize = 3
	var running, peak atomic.Int32
	server := &fakeServer{behavior: func(int) func(context.Context, *fakeSession) error {
		return func(context.Context, *fakeSession) error {
			n := running.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			return nil
		}
	}}
	p := newFakePool(maxSize, server)
	tl := workTool(t, p)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = call(tl, context.Background()) }()
	}
	wg.Wait()
	if got := len(server.created()); got > maxSize {
		t.Fatalf("created %d sessions, want at most %d", got, maxSize)
	}
	if got := peak.Load(); got > maxSize {
		t.Fatalf("peak concurrent calls = %d, want at most %d", got, maxSize)
	}
	if got := p.Size(); got > maxSize {
		t.Fatalf("Size() = %d, want at most %d", got, maxSize)
	}
}

func TestPoolWaitingCallerRespectsCancellation(t *testing.T) {
	hold := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	server := &fakeServer{behavior: func(int) func(context.Context, *fakeSession) error {
		return func(context.Context, *fakeSession) error {
			once.Do(func() { close(started) })
			<-hold
			return nil
		}
	}}
	p := newFakePool(1, server)
	tl := workTool(t, p)
	first := make(chan error, 1)
	go func() { first <- call(tl, context.Background()) }()
	<-started // the only session is now leased

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	begin := time.Now()
	err := call(tl, ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting call error = %v, want context deadline exceeded", err)
	}
	if time.Since(begin) > time.Second {
		t.Fatalf("waiting call took %s to notice cancellation", time.Since(begin))
	}
	// An explicit cancel also wakes a waiter.
	ctx2, cancel2 := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() { waiting <- call(tl, ctx2) }()
	time.Sleep(20 * time.Millisecond)
	cancel2()
	select {
	case err := <-waiting:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled waiter error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter never returned")
	}
	close(hold)
	if err := <-first; err != nil {
		t.Fatalf("leased call: %v", err)
	}
	if got := len(server.created()); got != 1 {
		t.Fatalf("created %d sessions, want 1: cancellation must not start extra subprocesses", got)
	}
	// The pool is still usable.
	if err := call(tl, context.Background()); err != nil {
		t.Fatalf("call after cancelled waiters: %v", err)
	}
}

func TestPoolCloseClosesEveryIdleSession(t *testing.T) {
	server := &fakeServer{}
	p := newFakePool(3, server)
	var idle []*fakeSession
	for i := 0; i < 3; i++ {
		s, _ := server.connect(context.Background())
		idle = append(idle, s.(*fakeSession))
		p.size++
		p.sessions <- s
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	for _, s := range idle {
		if !s.closed.Load() {
			t.Fatalf("idle session %d was not closed by Close", s.id)
		}
	}
	if got := p.Size(); got != 0 {
		t.Fatalf("Size() after Close = %d, want 0", got)
	}
}

func TestPoolCloseUnblocksWaitersAndClosesLeasedSessionOnRelease(t *testing.T) {
	hold := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	server := &fakeServer{behavior: func(int) func(context.Context, *fakeSession) error {
		return func(context.Context, *fakeSession) error { once.Do(func() { close(started) }); <-hold; return nil }
	}}
	p := newFakePool(1, server)
	tl := workTool(t, p)
	leased := make(chan error, 1)
	go func() { leased <- call(tl, context.Background()) }()
	<-started

	waiters := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() { waiters <- call(tl, context.Background()) }()
	}
	time.Sleep(30 * time.Millisecond)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		select {
		case err := <-waiters:
			if !errors.Is(err, errPoolClosed) {
				t.Fatalf("waiter error = %v, want the pool closed error", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not unblock every waiting caller")
		}
	}
	close(hold)
	if err := <-leased; err != nil {
		t.Fatalf("in-flight call must finish after Close: %v", err)
	}
	sessions := server.created()
	if len(sessions) != 1 || !sessions[0].closed.Load() {
		t.Fatalf("the leased session must be closed when it is released after Close (sessions=%d)", len(sessions))
	}
}

func TestPoolDoesNotReturnAFailedSessionToTheIdleSet(t *testing.T) {
	server := &fakeServer{behavior: func(n int) func(context.Context, *fakeSession) error {
		if n == 0 {
			return func(context.Context, *fakeSession) error { return io.ErrClosedPipe }
		}
		return nil
	}}
	p := newFakePool(2, server)
	tl := workTool(t, p)
	if err := call(tl, context.Background()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("first call error = %v, want the transport failure", err)
	}
	sessions := server.created()
	if !sessions[0].closed.Load() {
		t.Fatal("the failed session was not closed")
	}
	if got := len(p.sessions); got != 0 {
		t.Fatalf("idle sessions = %d, want 0: the failed session must not be reused", got)
	}
	if got := p.Size(); got != 0 {
		t.Fatalf("Size() = %d, want the failed session's slot freed", got)
	}
	// A later call starts a replacement and succeeds.
	if err := call(tl, context.Background()); err != nil {
		t.Fatalf("call after failure: %v", err)
	}
	if got := len(server.created()); got != 2 {
		t.Fatalf("created %d sessions, want a single replacement (2 total)", got)
	}
	if got := p.Size(); got != 1 {
		t.Fatalf("Size() = %d, want 1 live session", got)
	}
}

func TestPoolReplacesFailedSessionsBelowTheMaximum(t *testing.T) {
	// Every session fails its first call; the pool must keep creating
	// replacements without ever exceeding its maximum.
	var fails atomic.Int32
	server := &fakeServer{behavior: func(int) func(context.Context, *fakeSession) error {
		return func(context.Context, *fakeSession) error {
			if fails.Add(1) <= 4 {
				return io.EOF
			}
			return nil
		}
	}}
	p := newFakePool(2, server)
	tl := workTool(t, p)
	for i := 0; i < 4; i++ {
		if err := call(tl, context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("call %d error = %v, want transport failure", i, err)
		}
		if got := p.Size(); got != 0 {
			t.Fatalf("after failure %d Size() = %d, want 0", i, got)
		}
	}
	if err := call(tl, context.Background()); err != nil {
		t.Fatalf("call on a replacement session: %v", err)
	}
	if got := len(server.created()); got != 5 {
		t.Fatalf("created %d sessions, want 5 (4 failed + 1 replacement)", got)
	}
}

func TestPoolFreesTheSlotWhenAReplacementFailsToStart(t *testing.T) {
	server := &fakeServer{failConnect: map[int]bool{0: true}}
	p := newFakePool(1, server)
	tl := workTool(t, p)
	if err := call(tl, context.Background()); err == nil {
		t.Fatal("expected the failed start to surface")
	}
	if got := p.Size(); got != 0 {
		t.Fatalf("Size() = %d, want the reserved slot released", got)
	}
	if err := call(tl, context.Background()); err != nil {
		t.Fatalf("retry after a failed start: %v", err)
	}
}

func TestPoolKeepsSessionAfterServerAnswersWithAnRPCError(t *testing.T) {
	server := &fakeServer{behavior: func(int) func(context.Context, *fakeSession) error {
		return func(context.Context, *fakeSession) error {
			return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "bad arguments"}
		}
	}}
	p := newFakePool(1, server)
	tl := workTool(t, p)
	err := call(tl, context.Background())
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %v, want the JSON-RPC error", err)
	}
	if got := len(p.sessions); got != 1 || p.Size() != 1 || server.created()[0].closed.Load() {
		t.Fatalf("a healthy session that answered with an RPC error must stay pooled (idle=%d size=%d)", got, p.Size())
	}
}

func TestPoolDiscardsASessionWhoseCallWasCancelled(t *testing.T) {
	server := &fakeServer{behavior: func(int) func(context.Context, *fakeSession) error {
		return func(ctx context.Context, _ *fakeSession) error { <-ctx.Done(); return ctx.Err() }
	}}
	p := newFakePool(1, server)
	tl := workTool(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := call(tl, ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	// The abandoned request may still be running in that subprocess.
	if !server.created()[0].closed.Load() || len(p.sessions) != 0 || p.Size() != 0 {
		t.Fatal("a session with an abandoned in-flight request must not be reused")
	}
}
