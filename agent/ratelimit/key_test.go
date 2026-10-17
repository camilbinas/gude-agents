package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
)

// These tests drive a real agent loop and a real limiter to prove how the
// application-defined bucket key (agent.WithRateLimitKey) selects budgets.

type okProvider struct{ calls atomic.Int32 }

func (*okProvider) Name() string { return "ok" }
func (p *okProvider) Stream(_ context.Context, _ agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.calls.Add(1)
	return &agent.ModelResponse{Text: "ok", Usage: agent.TokenUsage{InputTokens: 1}}, nil
}

// keyRecorder wraps a limiter and records the bucket key of every attempt.
type keyRecorder struct {
	agent.RateLimiter
	mu   sync.Mutex
	keys []string
}

func (r *keyRecorder) Reserve(ctx context.Context, req agent.RateLimitRequest) (agent.RateLimitLease, error) {
	r.mu.Lock()
	r.keys = append(r.keys, req.Key)
	r.mu.Unlock()
	return r.RateLimiter.Reserve(ctx, req)
}

func (r *keyRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

func keyedAgent(t *testing.T, p agent.Provider, limiter agent.RateLimiter, opts ...agent.Option) *agent.Agent {
	t.Helper()
	opts = append([]agent.Option{agent.WithRateLimiter(limiter)}, opts...)
	a, err := agent.New(p, "sys", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func ctxFor(conversation, identity string) *agent.Context {
	c := agent.Background().WithConversationID(conversation)
	if identity != "" {
		c = c.WithIdentity(identity)
	}
	return c
}

func invoke(a *agent.Agent, c *agent.Context) error {
	_, err := a.Invoke(c, "hi")
	return err
}

func byIdentity(c *agent.Context) string { return c.Identity() }

func TestRateLimitKeySameIdentityAcrossConversationsSharesBudget(t *testing.T) {
	a := keyedAgent(t, &okProvider{}, limiterForTest(t, RPM(2)), agent.WithRateLimitKey(byIdentity))
	if err := invoke(a, ctxFor("conv-1", "acme")); err != nil {
		t.Fatal(err)
	}
	if err := invoke(a, ctxFor("conv-2", "acme")); err != nil {
		t.Fatal(err)
	}
	if err := invoke(a, ctxFor("conv-3", "acme")); !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("third acme request in a new conversation = %v, want ErrRateLimitExceeded", err)
	}
}

func TestRateLimitKeyDifferentIdentitiesAreIndependent(t *testing.T) {
	a := keyedAgent(t, &okProvider{}, limiterForTest(t, RPM(2)), agent.WithRateLimitKey(byIdentity))
	for i := 0; i < 2; i++ {
		if err := invoke(a, ctxFor("same-conversation", "acme")); err != nil {
			t.Fatal(err)
		}
	}
	// acme is exhausted, even from a brand new conversation ...
	if err := invoke(a, ctxFor("other", "acme")); !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("exhausted identity = %v, want ErrRateLimitExceeded", err)
	}
	// ... but globex has its own untouched budget, even in acme's conversation.
	for i := 0; i < 2; i++ {
		if err := invoke(a, ctxFor("same-conversation", "globex")); err != nil {
			t.Fatalf("globex request %d: %v", i+1, err)
		}
	}
}

func TestRateLimitWithoutSelectorUsesOneSharedEmptyKeyBucket(t *testing.T) {
	recorder := &keyRecorder{RateLimiter: limiterForTest(t, RPM(2))}
	a := keyedAgent(t, &okProvider{}, recorder)
	// Different conversations and identities all land in the same bucket: the
	// engine neither falls back to the conversation ID nor to the identity.
	if err := invoke(a, ctxFor("conv-1", "acme")); err != nil {
		t.Fatal(err)
	}
	if err := invoke(a, ctxFor("conv-2", "globex")); err != nil {
		t.Fatal(err)
	}
	if err := invoke(a, ctxFor("conv-3", "initech")); !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("third request from a third tenant = %v, want the shared bucket to be exhausted", err)
	}
	for i, key := range recorder.recorded() {
		if key != "" {
			t.Fatalf("attempt %d key = %q, want the empty key", i, key)
		}
	}
}

func TestRateLimitGlobalLimitCoversEveryKey(t *testing.T) {
	a := keyedAgent(t, &okProvider{}, limiterForTest(t, RPM(10), WithGlobalRPM(3)), agent.WithRateLimitKey(byIdentity))
	for _, identity := range []string{"acme", "globex", "initech"} {
		if err := invoke(a, ctxFor("c-"+identity, identity)); err != nil {
			t.Fatalf("%s: %v", identity, err)
		}
	}
	// Every per-identity budget has room left, but the global limit is spent.
	for _, identity := range []string{"acme", "umbrella"} {
		if err := invoke(a, ctxFor("late-"+identity, identity)); !errors.Is(err, agent.ErrRateLimitExceeded) {
			t.Fatalf("%s after the global limit = %v, want ErrRateLimitExceeded", identity, err)
		}
	}
}

func TestRateLimitKeyIsEvaluatedForEveryPhysicalAttempt(t *testing.T) {
	recorder := &keyRecorder{RateLimiter: limiterForTest(t, RPM(100))}
	var selections atomic.Int32
	a := keyedAgent(t, &okProvider{}, recorder, agent.WithRateLimitKey(func(c *agent.Context) string {
		selections.Add(1)
		return "tenant:" + c.Identity()
	}))
	for _, identity := range []string{"acme", "globex", "acme"} {
		if err := invoke(a, ctxFor("c", identity)); err != nil {
			t.Fatal(err)
		}
	}
	if got := selections.Load(); got != 3 {
		t.Fatalf("selector ran %d times for 3 attempts", got)
	}
	want := []string{"tenant:acme", "tenant:globex", "tenant:acme"}
	got := recorder.recorded()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
}

func TestRateLimitedAttemptsAreNeverRetriedByTheAgent(t *testing.T) {
	p := &failingProvider{}
	recorder := &keyRecorder{RateLimiter: limiterForTest(t, RPM(100))}
	a := keyedAgent(t, p, recorder, agent.WithRateLimitKey(byIdentity))
	if err := invoke(a, ctxFor("c", "acme")); err == nil {
		t.Fatal("expected the provider error")
	}
	if p.calls.Load() != 1 || len(recorder.recorded()) != 1 {
		t.Fatalf("provider calls=%d reservations=%d, want one physical attempt", p.calls.Load(), len(recorder.recorded()))
	}
}
