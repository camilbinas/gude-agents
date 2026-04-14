package prompt

import "strings"

// Instructions is the interface for anything that can produce a system prompt string.
// RISEN, COSTAR, and Text all implement this interface.
// Documented in docs/prompts.md — update when changing interface or implementations.
type Instructions interface {
	String() string
}

// Text is a plain-string system prompt. It implements Instructions.
// Documented in docs/prompts.md — update when changing behavior.
type Text string

func (t Text) String() string { return string(t) }

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
