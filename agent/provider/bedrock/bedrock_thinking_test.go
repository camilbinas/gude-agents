package bedrock

import (
	"net/netip"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/camilbinas/gude-agents/agent"
)

func TestApplyStreamEvent_CapturesSignedAndRedactedThinking(t *testing.T) {
	resp := &agent.ModelResponse{}
	state := streamState{}
	delta := func(d types.ReasoningContentBlockDelta) types.ConverseStreamOutput {
		return &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{
			Delta: &types.ContentBlockDeltaMemberReasoningContent{Value: d},
		}}
	}
	stop := &types.ConverseStreamOutputMemberContentBlockStop{}
	applyStreamEvent(resp, &state, delta(&types.ReasoningContentBlockDeltaMemberText{Value: "think"}), nil)
	applyStreamEvent(resp, &state, delta(&types.ReasoningContentBlockDeltaMemberSignature{Value: "sig"}), nil)
	applyStreamEvent(resp, &state, stop, nil)
	applyStreamEvent(resp, &state, delta(&types.ReasoningContentBlockDeltaMemberRedactedContent{Value: []byte("xyz")}), nil)
	applyStreamEvent(resp, &state, stop, nil)

	if len(resp.Thinking) != 2 {
		t.Fatalf("Thinking = %#v", resp.Thinking)
	}
	if resp.Thinking[0] != (agent.ThinkingBlock{Text: "think", Signature: "sig"}) {
		t.Errorf("block0 = %#v", resp.Thinking[0])
	}
	if resp.Thinking[1].RedactedData == "" {
		t.Errorf("block1 = %#v", resp.Thinking[1])
	}
	if resp.Metadata["thinking"] != "think" {
		t.Errorf("metadata = %#v", resp.Metadata)
	}

	// Round trip back into Bedrock content blocks, thinking first.
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		resp.Thinking[0], resp.Thinking[1], agent.ThinkingBlock{Text: "unsigned"},
		agent.TextBlock{Text: "x"},
	}, "anthropic.claude-sonnet-4", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks", len(blocks))
	}
	rt, ok := blocks[0].(*types.ContentBlockMemberReasoningContent)
	if !ok {
		t.Fatalf("block0 type %T", blocks[0])
	}
	txt := rt.Value.(*types.ReasoningContentBlockMemberReasoningText).Value
	if *txt.Text != "think" || *txt.Signature != "sig" {
		t.Errorf("reasoning text = %#v", txt)
	}
	red := blocks[1].(*types.ContentBlockMemberReasoningContent).Value.(*types.ReasoningContentBlockMemberRedactedContent)
	if string(red.Value) != "xyz" {
		t.Errorf("redacted = %q", red.Value)
	}
}

func TestValidateURLFetchIP_ReservedRanges(t *testing.T) {
	for _, s := range []string{"100.64.0.1", "100.127.255.254", "0.1.2.3", "64:ff9b::808:808", "2002:808:808::1", "::ffff:100.64.0.1"} {
		if err := validateURLFetchIP(netip.MustParseAddr(s), urlFetchPolicy{allowPrivateNetworks: true}); err == nil {
			t.Errorf("%s was allowed", s)
		}
	}
	if err := validateURLFetchIP(netip.MustParseAddr("8.8.8.8"), urlFetchPolicy{}); err != nil {
		t.Errorf("public IP rejected: %v", err)
	}
}
