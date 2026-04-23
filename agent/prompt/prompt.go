package prompt

import "strings"

// Instructions is the interface for anything that can produce a system prompt string.
// Text, RISEN, COSTAR, APE, and TRACE all implement this interface.
// Implement it yourself for custom prompt frameworks.
// Documented in docs/prompts.md — update when changing interface or implementations.
type Instructions interface {
	String() string
}

// Text is a plain-string system prompt. It implements Instructions.
// Documented in docs/prompts.md — update when changing behavior.
type Text string

func (t Text) String() string { return string(t) }

// APE builds a system prompt using the APE framework.
// Action, Purpose, Expectation — a concise format for tool-heavy agents.
// Documented in docs/prompts.md — update when changing fields or output format.
type APE struct {
	Action      string
	Purpose     string
	Expectation string
}

func (a APE) String() string {
	var sb strings.Builder
	if a.Action != "" {
		sb.WriteString("Action: ")
		sb.WriteString(a.Action)
	}
	if a.Purpose != "" {
		sb.WriteString("\nPurpose: ")
		sb.WriteString(a.Purpose)
	}
	if a.Expectation != "" {
		sb.WriteString("\nExpectation: ")
		sb.WriteString(a.Expectation)
	}
	return sb.String()
}

// RISEN builds a system prompt using the RISEN framework.
// Role, Instructions, Steps, End goal, Narrowing.
// Documented in docs/prompts.md — update when changing fields or output format.
type RISEN struct {
	Role         string
	Instructions string
	Steps        string
	EndGoal      string
	Narrowing    string
}

func (r RISEN) String() string {
	var sb strings.Builder
	if r.Role != "" {
		sb.WriteString("Role: ")
		sb.WriteString(r.Role)
	}
	if r.Instructions != "" {
		sb.WriteString("\nInstructions: ")
		sb.WriteString(r.Instructions)
	}
	if r.Steps != "" {
		sb.WriteString("\nSteps: ")
		sb.WriteString(r.Steps)
	}
	if r.EndGoal != "" {
		sb.WriteString("\nEnd goal: ")
		sb.WriteString(r.EndGoal)
	}
	if r.Narrowing != "" {
		sb.WriteString("\nNarrowing: ")
		sb.WriteString(r.Narrowing)
	}
	return sb.String()
}

// COSTAR builds a system prompt using the CO-STAR framework.
// Context, Objective, Style, Tone, Audience, Response format.
// Documented in docs/prompts.md — update when changing fields or output format.
type COSTAR struct {
	Context   string
	Objective string
	Style     string
	Tone      string
	Audience  string
	Response  string
}

func (c COSTAR) String() string {
	var sb strings.Builder
	if c.Context != "" {
		sb.WriteString("Context: ")
		sb.WriteString(c.Context)
	}
	if c.Objective != "" {
		sb.WriteString("\nObjective: ")
		sb.WriteString(c.Objective)
	}
	if c.Style != "" {
		sb.WriteString("\nStyle: ")
		sb.WriteString(c.Style)
	}
	if c.Tone != "" {
		sb.WriteString("\nTone: ")
		sb.WriteString(c.Tone)
	}
	if c.Audience != "" {
		sb.WriteString("\nAudience: ")
		sb.WriteString(c.Audience)
	}
	if c.Response != "" {
		sb.WriteString("\nResponse format: ")
		sb.WriteString(c.Response)
	}
	return sb.String()
}

// TRACE builds a system prompt using the TRACE framework.
// Task, Request, Action, Context, Example — useful when you want to show
// the model what good output looks like.
// Documented in docs/prompts.md — update when changing fields or output format.
type TRACE struct {
	Task    string
	Request string
	Action  string
	Context string
	Example string
}

func (tr TRACE) String() string {
	var sb strings.Builder
	if tr.Task != "" {
		sb.WriteString("Task: ")
		sb.WriteString(tr.Task)
	}
	if tr.Request != "" {
		sb.WriteString("\nRequest: ")
		sb.WriteString(tr.Request)
	}
	if tr.Action != "" {
		sb.WriteString("\nAction: ")
		sb.WriteString(tr.Action)
	}
	if tr.Context != "" {
		sb.WriteString("\nContext: ")
		sb.WriteString(tr.Context)
	}
	if tr.Example != "" {
		sb.WriteString("\nExample: ")
		sb.WriteString(tr.Example)
	}
	return sb.String()
}
