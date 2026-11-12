package anthropic

import (
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/camilbinas/gude-agents/agent"
)

func TestNormalizeStopReason(t *testing.T) {
	cases := map[anthropicsdk.StopReason]agent.ModelStopReason{
		"":                              agent.ModelStopUnknown,
		"end_turn":                      agent.ModelStopEndTurn,
		"tool_use":                      agent.ModelStopToolUse,
		"max_tokens":                    agent.ModelStopMaxTokens,
		"stop_sequence":                 agent.ModelStopSequence,
		"refusal":                       agent.ModelStopContentFilter,
		"model_context_window_exceeded": agent.ModelStopContextWindow,
		"pause_turn":                    agent.ModelStopOther,
	}
	for raw, want := range cases {
		if got := normalizeStopReason(raw); got != want {
			t.Errorf("normalizeStopReason(%q) = %q, want %q", raw, got, want)
		}
	}
}
