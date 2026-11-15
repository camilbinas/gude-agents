package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// codecExecStore is a minimal ExecutionStore; agent/execution is a separate
// module the root module's tests cannot import.
type codecExecStore struct{ m map[string]agent.Execution }

func (s *codecExecStore) Create(_ context.Context, e agent.Execution) (agent.Execution, error) {
	if _, ok := s.m[e.ID]; ok {
		return agent.Execution{}, agent.ErrExecutionConflict
	}
	e.Version = 1
	s.m[e.ID] = e
	return e, nil
}

func (s *codecExecStore) Load(_ context.Context, id string) (agent.Execution, error) {
	e, ok := s.m[id]
	if !ok {
		return agent.Execution{}, agent.ErrExecutionNotFound
	}
	return e, nil
}

func (s *codecExecStore) Save(_ context.Context, e agent.Execution, expected uint64) (agent.Execution, error) {
	cur, ok := s.m[e.ID]
	if !ok || cur.Version != expected {
		return agent.Execution{}, agent.ErrExecutionConflict
	}
	e.Version = expected + 1
	s.m[e.ID] = e
	return e, nil
}

// The snapshot carries messages in its own JSON envelope (agent cannot import
// conversation). This guards against the two encodings drifting apart.
func TestSnapshotMessagesMatchConversationCodec(t *testing.T) {
	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
			agent.TextBlock{Text: "calling"},
			agent.ToolUseBlock{ToolUseID: "t1", Name: "lookup", Input: json.RawMessage(`{"q":"x"}`)},
		}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.ToolResultBlock{ToolUseID: "t1", Content: "ok"}}},
	}
	approval := tool.New("lookup", "look up", func(context.Context, struct {
		Q string `json:"q"`
	}) (string, error) {
		return "ok", nil
	}, tool.RequiresApproval())

	conversations := conversation.NewInMemory()
	if _, err := conversations.Append(context.Background(), "c1", msgs, 0); err != nil {
		t.Fatal(err)
	}
	src, err := agent.Agent(testutil.NewMockProvider(testutil.WithResponses(
		&agent.ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "t2", Name: "lookup", Input: json.RawMessage(`{"q":"y"}`)}}},
	)), "sys",
		agent.WithConversationStore(conversations),
		agent.WithExecutionStore(&codecExecStore{m: map[string]agent.Execution{}}),
		agent.WithTools(approval),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := src.Invoke(agent.Background().WithConversationID("c1"), "again")
	if err != nil || res.Interrupt == nil {
		t.Fatalf("want a pause, got %+v, %v", res, err)
	}
	blob, err := src.Snapshot(context.Background(), res.Interrupt)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(blob, &envelope); err != nil {
		t.Fatal(err)
	}
	snap, err := conversations.Load(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	want, err := conversation.MarshalMessages(snap.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, envelope.Messages, want) {
		t.Fatalf("snapshot messages differ from the conversation codec:\n got %s\nwant %s", envelope.Messages, want)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
