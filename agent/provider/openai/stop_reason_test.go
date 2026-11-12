package openai

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	openaisdk "github.com/openai/openai-go/v3"
)

func TestNormalizeFinishReason(t *testing.T) {
	cases := map[string]agent.ModelStopReason{
		"":               agent.ModelStopUnknown,
		"stop":           agent.ModelStopEndTurn,
		"tool_calls":     agent.ModelStopToolUse,
		"function_call":  agent.ModelStopToolUse,
		"length":         agent.ModelStopMaxTokens,
		"content_filter": agent.ModelStopContentFilter,
		"something_new":  agent.ModelStopOther,
	}
	for raw, want := range cases {
		if got := normalizeFinishReason(raw); got != want {
			t.Errorf("normalizeFinishReason(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseCompletionKeepsFinishReason(t *testing.T) {
	completion := openaisdk.ChatCompletion{Choices: []openaisdk.ChatCompletionChoice{{
		FinishReason: "length",
		Message:      openaisdk.ChatCompletionMessage{Content: "cut"},
	}}}
	resp := parseCompletion(&completion)
	if resp.StopReason != agent.ModelStopMaxTokens || resp.RawStopReason != "length" {
		t.Fatalf("StopReason = %q, Raw = %q", resp.StopReason, resp.RawStopReason)
	}
}
