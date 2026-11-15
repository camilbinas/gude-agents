package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// reviewAnswer is the typed answer R a human returns for an edit/approve pause.
type reviewAnswer struct {
	Approved bool   `json:"approved" required:"true"`
	Edited   string `json:"edited"`
}

// typedAskTool builds a NewHumanInput[reviewAnswer] that records the answer it
// receives and turns it into a user-visible message.
func typedAskTool(seen *reviewAnswer) tool.Tool {
	return NewHumanInput("ask_review", "ask a human to approve or edit",
		func(_ *Context, a reviewAnswer) (string, error) {
			if seen != nil {
				*seen = a
			}
			if !a.Approved {
				return "Human rejected the draft.", nil
			}
			return "Human approved: " + a.Edited, nil
		})
}

// TestNewHumanInput_PausesWithSchema verifies the pause carries R's schema.
func TestNewHumanInput_PausesWithSchema(t *testing.T) {
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
		ToolUseID: "h-1", Name: "ask_review", Input: json.RawMessage(`{"reason":"need sign-off","question":"approve?"}`),
	}}})
	_, _, stores := pauseStores()
	a, err := New(provider, "helpful", append(stores, WithTools(typedAskTool(nil)))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background().WithConversationID("typed-pause"), "review draft")
	if in.Type != InterruptHumanInput || in.Input == nil {
		t.Fatalf("interrupt = %+v, want human_input with input", in)
	}
	if in.Input.Tool != "ask_review" {
		t.Fatalf("Input.Tool = %q, want ask_review", in.Input.Tool)
	}
	var schema map[string]any
	if err := json.Unmarshal(in.Input.Schema, &schema); err != nil {
		t.Fatalf("Input.Schema is not valid JSON: %v (%q)", err, in.Input.Schema)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["approved"]; !ok {
		t.Fatalf("schema missing 'approved' property: %v", schema)
	}
	req, _ := schema["required"].([]any)
	if len(req) != 1 || req[0] != "approved" {
		t.Fatalf("schema required = %v, want [approved]", schema["required"])
	}
}

// TestNewHumanInput_RespondWith_DeliversTypedValue resumes with a typed value
// and checks the handler saw it and the run continued.
func TestNewHumanInput_RespondWith_DeliversTypedValue(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "h-1", Name: "ask_review", Input: json.RawMessage(`{"reason":"r","question":"q"}`)}}},
		&ModelResponse{Text: "Final: shipped."},
	)
	var seen reviewAnswer
	_, _, stores := pauseStores()
	a, err := New(provider, "helpful", append(stores, WithTools(typedAskTool(&seen)))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background().WithConversationID("typed-respond"), "review")

	res, err := a.Resume(Background(), in, RespondWith(reviewAnswer{Approved: true, Edited: "ship it"}))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.Text != "Final: shipped." || res.StopReason != StopEndTurn {
		t.Fatalf("result = %+v", res)
	}
	if !seen.Approved || seen.Edited != "ship it" {
		t.Fatalf("handler saw %+v, want {true, ship it}", seen)
	}
}

// TestNewHumanInput_RejectsSchemaInvalidAnswer verifies a bad typed value is
// rejected with a *ResumeValidationError and the run stays paused.
func TestNewHumanInput_RejectsSchemaInvalidAnswer(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "h-1", Name: "ask_review", Input: json.RawMessage(`{"reason":"r","question":"q"}`)}}},
		&ModelResponse{Text: "unused"},
	)
	conversations, executions, stores := pauseStores()
	a, err := New(provider, "helpful", append(stores, WithTools(typedAskTool(nil)))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background().WithConversationID("typed-bad"), "review")

	// "approved" must be a boolean; a string violates the type.
	bad := RespondWith(map[string]any{"approved": "yes"})
	_, err = a.Resume(Background(), in, bad)
	var verr *ResumeValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("Resume error = %v, want *ResumeValidationError", err)
	}

	// The run is still paused: the projection still resolves and a valid answer
	// now resumes it.
	reloaded, lerr := a.LoadInterrupt(context.Background(), in.ExecutionID)
	if lerr != nil {
		t.Fatalf("LoadInterrupt after rejected resume: %v", lerr)
	}
	_ = conversations
	_ = executions
	if reloaded.Type != InterruptHumanInput {
		t.Fatalf("reloaded interrupt = %+v, want still paused on human_input", reloaded)
	}
}

// TestNewHumanInput_FreeTextRespondRejected verifies a typed pause refuses a
// free-text Respond, directing the caller to RespondWith.
func TestNewHumanInput_FreeTextRespondRejected(t *testing.T) {
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
		ToolUseID: "h-1", Name: "ask_review", Input: json.RawMessage(`{"reason":"r","question":"q"}`),
	}}})
	_, _, stores := pauseStores()
	a, err := New(provider, "helpful", append(stores, WithTools(typedAskTool(nil)))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background().WithConversationID("typed-freetext"), "review")
	_, err = a.Resume(Background(), in, Respond("just text"))
	if err == nil || !strings.Contains(err.Error(), "RespondWith") {
		t.Fatalf("Resume(Respond) error = %v, want a message pointing at RespondWith", err)
	}
}

// TestNewHumanInput_SnapshotRestoreTypedResume is the two-process test: pause,
// Snapshot, Restore into a fresh runtime with fresh stores, resume there with a
// typed value, and verify the schema and typed answer survived both hops.
func TestNewHumanInput_SnapshotRestoreTypedResume(t *testing.T) {
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
		ToolUseID: "h-1", Name: "ask_review", Input: json.RawMessage(`{"reason":"r","question":"approve the edit?"}`),
	}}})
	_, _, stores := pauseStores()
	src, err := New(provider, "helpful", append(stores, WithTools(typedAskTool(nil)))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, src, Background().WithConversationID("typed-snap"), "review")
	if len(in.Input.Schema) == 0 {
		t.Fatal("pause carried no answer schema")
	}

	blob, err := src.Snapshot(context.Background(), in)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Fresh process: new runtime, new stores. Only the blob crosses over.
	var seen reviewAnswer
	dst, err := New(
		newScriptedProvider(&ModelResponse{Text: "Final: edited draft shipped."}),
		"helpful",
		WithConversationStore(newTestMemoryStore()), WithExecutionStore(newTestExecutionStore()),
		WithTools(typedAskTool(&seen)),
	)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := dst.Restore(context.Background(), blob)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Type != InterruptHumanInput || restored.Input == nil || len(restored.Input.Schema) == 0 {
		t.Fatalf("restored interrupt lost its schema: %+v", restored)
	}
	if restored.Input.Tool != "ask_review" {
		t.Fatalf("restored Input.Tool = %q, want ask_review", restored.Input.Tool)
	}

	res, err := dst.Resume(Background(), restored, RespondWith(reviewAnswer{Approved: true, Edited: "final draft"}))
	if err != nil {
		t.Fatalf("Resume after restore: %v", err)
	}
	if res.Text != "Final: edited draft shipped." {
		t.Fatalf("resumed result = %+v", res)
	}
	if !seen.Approved || seen.Edited != "final draft" {
		t.Fatalf("handler after restore saw %+v, want {true, final draft}", seen)
	}
}
