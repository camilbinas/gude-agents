package agent

import (
	"reflect"
	"strings"
	"testing"
)

// Agent must return the Runtime type; this fails to compile otherwise.
var _ func(Provider, string, ...Option) (*Runtime, error) = Agent

// constructors lists both spellings of the constructor so every case below
// runs against New and Agent with identical arguments.
var constructors = []struct {
	name string
	ctor func(Provider, string, ...Option) (*Runtime, error)
}{
	{"New", New},
	{"Agent", Agent},
}

func TestAgentConstructorMatchesNew(t *testing.T) {
	build := func(ctor func(Provider, string, ...Option) (*Runtime, error)) *Runtime {
		t.Helper()
		a, err := ctor(newScriptedProvider(), "be brief",
			WithName("helper"),
			WithTools(dummyTool("alpha", "first"), dummyTool("beta", "second")),
			WithMaxIterations(3),
		)
		if err != nil {
			t.Fatalf("constructor returned an error: %v", err)
		}
		return a
	}

	viaNew, viaAgent := build(New), build(Agent)

	if viaNew.Name() != viaAgent.Name() || viaAgent.Name() != "helper" {
		t.Errorf("Name differs: New=%q Agent=%q", viaNew.Name(), viaAgent.Name())
	}
	if viaNew.Instructions() != viaAgent.Instructions() || viaAgent.Instructions() != "be brief" {
		t.Errorf("Instructions differ: New=%q Agent=%q", viaNew.Instructions(), viaAgent.Instructions())
	}
	if !reflect.DeepEqual(viaNew.ToolSpecs(), viaAgent.ToolSpecs()) || len(viaAgent.ToolSpecs()) != 2 {
		t.Errorf("ToolSpecs differ: New=%v Agent=%v", viaNew.ToolSpecs(), viaAgent.ToolSpecs())
	}
	if viaNew.maxIterations != viaAgent.maxIterations || viaAgent.maxIterations != 3 {
		t.Errorf("maxIterations differ: New=%d Agent=%d", viaNew.maxIterations, viaAgent.maxIterations)
	}
}

func TestAgentConstructorInvokesLikeNew(t *testing.T) {
	for _, c := range constructors {
		t.Run(c.name, func(t *testing.T) {
			sp := newScriptedProvider(&ModelResponse{Text: "pong"})
			a, err := c.ctor(sp, "sys")
			if err != nil {
				t.Fatal(err)
			}
			res, err := a.Invoke(Background(), "ping")
			if err != nil {
				t.Fatal(err)
			}
			if res.Text != "pong" {
				t.Errorf("Text = %q, want pong", res.Text)
			}
		})
	}
}

func TestAgentConstructorRejectsNilProviderLikeNew(t *testing.T) {
	var errs []string
	for _, c := range constructors {
		a, err := c.ctor(nil, "sys")
		if a != nil || err == nil {
			t.Fatalf("%s(nil provider) = (%v, %v), want nil and an error", c.name, a, err)
		}
		errs = append(errs, err.Error())
	}
	if errs[0] != errs[1] || !strings.Contains(errs[0], "provider is required") {
		t.Errorf("nil provider errors differ: New=%q Agent=%q", errs[0], errs[1])
	}
}

func TestAgentConstructorInvalidOptionLikeNew(t *testing.T) {
	var errs []string
	for _, c := range constructors {
		a, err := c.ctor(newScriptedProvider(), "sys", WithMaxIterations(0))
		if a != nil || err == nil {
			t.Fatalf("%s(invalid option) = (%v, %v), want nil and an error", c.name, a, err)
		}
		errs = append(errs, err.Error())
	}
	if errs[0] != errs[1] {
		t.Errorf("invalid option errors differ: New=%q Agent=%q", errs[0], errs[1])
	}
}

func TestAgentConstructorSkipsNilOptionLikeNew(t *testing.T) {
	for _, c := range constructors {
		a, err := c.ctor(newScriptedProvider(), "sys", nil, WithName("n"))
		if err != nil || a == nil || a.Name() != "n" {
			t.Errorf("%s with a nil option = (%v, %v)", c.name, a, err)
		}
	}
}

func TestAgentConstructorAcceptsConversationAndExecutionStores(t *testing.T) {
	for _, c := range constructors {
		t.Run(c.name, func(t *testing.T) {
			_, _, opts := pauseStores()
			a, err := c.ctor(newScriptedProvider(), "sys", opts...)
			if err != nil || a == nil {
				t.Fatalf("%s with conversation and execution stores = (%v, %v)", c.name, a, err)
			}
			if a.conversation == nil || a.executionStore == nil {
				t.Error("stores were not applied")
			}
		})
	}
}

func TestAgentConstructorRejectsExecutionStoreWithoutConversationLikeNew(t *testing.T) {
	var errs []string
	for _, c := range constructors {
		_, err := c.ctor(newScriptedProvider(), "sys", WithExecutionStore(newTestExecutionStore()))
		if err == nil {
			t.Fatalf("%s accepted an ExecutionStore without a ConversationStore", c.name)
		}
		errs = append(errs, err.Error())
	}
	if errs[0] != errs[1] {
		t.Errorf("errors differ: New=%q Agent=%q", errs[0], errs[1])
	}
}

func TestAgentConstructorRejectsToolExecutionConflictLikeNew(t *testing.T) {
	var errs []string
	for _, c := range constructors {
		_, err := c.ctor(newScriptedProvider(), "sys",
			WithToolExecution(ToolExecutionSequential),
			WithMaxConcurrentTools(4),
		)
		if err == nil {
			t.Fatalf("%s accepted WithToolExecution(Sequential) with WithMaxConcurrentTools(4)", c.name)
		}
		errs = append(errs, err.Error())
	}
	if errs[0] != errs[1] || !strings.Contains(errs[0], "conflicts with WithMaxConcurrentTools(4)") {
		t.Errorf("conflict errors differ or are unexpected: New=%q Agent=%q", errs[0], errs[1])
	}
}

func TestAgentConstructorWorksWithAgentAsTool(t *testing.T) {
	child, err := Agent(newScriptedProvider(&ModelResponse{Text: "child answer"}), "child", WithName("child"))
	if err != nil {
		t.Fatal(err)
	}
	if got := AgentAsTool("child", "delegates to the child", child); got.Spec.Name != "child" {
		t.Fatalf("AgentAsTool spec name = %q, want child", got.Spec.Name)
	}
}
