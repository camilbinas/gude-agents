package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/camilbinas/gude-agents/agent"
)

func TestNormalizeStopReason(t *testing.T) {
	cases := map[types.StopReason]agent.ModelStopReason{
		"":                                         agent.ModelStopUnknown,
		types.StopReasonEndTurn:                    agent.ModelStopEndTurn,
		types.StopReasonToolUse:                    agent.ModelStopToolUse,
		types.StopReasonMaxTokens:                  agent.ModelStopMaxTokens,
		types.StopReasonStopSequence:               agent.ModelStopSequence,
		types.StopReasonGuardrailIntervened:        agent.ModelStopContentFilter,
		types.StopReasonContentFiltered:            agent.ModelStopContentFilter,
		types.StopReasonModelContextWindowExceeded: agent.ModelStopContextWindow,
		types.StopReasonMalformedToolUse:           agent.ModelStopOther,
	}
	for raw, want := range cases {
		if got := normalizeStopReason(raw); got != want {
			t.Errorf("normalizeStopReason(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestApplyStreamEventRecordsMessageStop(t *testing.T) {
	resp := &agent.ModelResponse{}
	applyStreamEvent(resp, &streamState{}, &types.ConverseStreamOutputMemberMessageStop{
		Value: types.MessageStopEvent{StopReason: types.StopReasonMaxTokens},
	}, nil)
	if resp.StopReason != agent.ModelStopMaxTokens || resp.RawStopReason != "max_tokens" {
		t.Fatalf("StopReason = %q, Raw = %q", resp.StopReason, resp.RawStopReason)
	}
}
