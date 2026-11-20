package agent

import (
	"reflect"
	"testing"
)

func TestThinkingContent_OnlyReplayableInOrder(t *testing.T) {
	r := &ModelResponse{Thinking: []ThinkingBlock{
		{Text: "a", Signature: "sig-a"},
		{Text: "unsigned"},
		{RedactedData: "opaque"},
	}}
	got := r.ThinkingContent()
	want := []ContentBlock{
		ThinkingBlock{Text: "a", Signature: "sig-a"},
		ThinkingBlock{RedactedData: "opaque"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if (*ModelResponse)(nil).ThinkingContent() != nil {
		t.Fatal("nil response must yield nil")
	}
}

func TestSnapshotCodec_ThinkingRoundTrip(t *testing.T) {
	in := []Message{{Role: RoleAssistant, Content: []ContentBlock{
		ThinkingBlock{Text: "t", Signature: "s"},
		ThinkingBlock{RedactedData: "r"},
		TextBlock{Text: "x"},
	}}}
	out, err := unmarshalMessages(marshalMessages(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("got %#v want %#v", out, in)
	}
}

func TestCharEstimator_CountsThinking(t *testing.T) {
	req := ModelRequest{Messages: []Message{{Role: RoleAssistant, Content: []ContentBlock{ThinkingBlock{Text: "abcdefgh"}}}}}
	n, err := CharEstimator{}.EstimateTokens(nil, req)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
