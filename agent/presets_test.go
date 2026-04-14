package agent

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestDefault_SetsMaxIterations5(t *testing.T) {
	a, err := Default(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.maxIterations != 5 {
		t.Errorf("expected maxIterations=5, got %d", a.maxIterations)
	}
}

func TestDefault_AcceptsExtraOptions(t *testing.T) {
	a, err := Default(mockProvider{}, prompt.Text("sys"), nil, WithMaxIterations(20))
	if err != nil {
		t.Fatal(err)
	}
	// Extra option overrides the default.
	if a.maxIterations != 20 {
		t.Errorf("expected maxIterations=20 (overridden), got %d", a.maxIterations)
	}
}

func TestWorker_SetsMaxIterations3(t *testing.T) {
	a, err := Worker(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.maxIterations != 3 {
		t.Errorf("expected maxIterations=3, got %d", a.maxIterations)
	}
}

func TestWorker_SetsLogger(t *testing.T) {
	a, err := Worker(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.logger == nil {
		t.Error("expected logger to be set")
	}
}

func TestWorker_AcceptsExtraOptions(t *testing.T) {
	a, err := Worker(mockProvider{}, prompt.Text("sys"), nil, WithMaxIterations(1))
	if err != nil {
		t.Fatal(err)
	}
	if a.maxIterations != 1 {
		t.Errorf("expected maxIterations=1 (overridden), got %d", a.maxIterations)
	}
}

func TestOrchestrator_SetsMaxIterations5(t *testing.T) {
	a, err := Orchestrator(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.maxIterations != 5 {
		t.Errorf("expected maxIterations=5, got %d", a.maxIterations)
	}
}

func TestOrchestrator_SetsParallelTools(t *testing.T) {
	a, err := Orchestrator(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.parallelTools {
		t.Error("expected parallelTools=true")
	}
}

func TestOrchestrator_SetsLogger(t *testing.T) {
	a, err := Orchestrator(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.logger == nil {
		t.Error("expected logger to be set")
	}
}

func TestOrchestrator_AcceptsExtraOptions(t *testing.T) {
	a, err := Orchestrator(mockProvider{}, prompt.Text("sys"), nil, WithTokenBudget(1000))
	if err != nil {
		t.Fatal(err)
	}
	if a.tokenBudget != 1000 {
		t.Errorf("expected tokenBudget=1000, got %d", a.tokenBudget)
	}
}

func TestWorker_WithTools(t *testing.T) {
	searchTool := dummyTool("search", "Search things")
	a, err := Worker(mockProvider{}, prompt.Text("sys"), []tool.Tool{searchTool})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(a.tools))
	}
	if a.maxIterations != 3 {
		t.Errorf("expected maxIterations=3, got %d", a.maxIterations)
	}
}
