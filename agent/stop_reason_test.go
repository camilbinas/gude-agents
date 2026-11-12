package agent

import "testing"

func TestInvokeSurfacesProviderStopReason(t *testing.T) {
	cases := []struct {
		name   string
		reason ModelStopReason
		want   StopReason
	}{
		{"unreported", ModelStopUnknown, StopEndTurn},
		{"end turn", ModelStopEndTurn, StopEndTurn},
		{"stop sequence", ModelStopSequence, StopEndTurn},
		{"max tokens", ModelStopMaxTokens, StopMaxTokens},
		{"context window", ModelStopContextWindow, StopMaxTokens},
		{"content filter", ModelStopContentFilter, StopContentFilter},
		{"other", ModelStopOther, StopEndTurn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sp := newScriptedProvider(&ModelResponse{Text: "partial", StopReason: tc.reason, RawStopReason: "raw"})
			a, err := New(sp, "sys")
			if err != nil {
				t.Fatal(err)
			}
			res, err := a.Invoke(Background(), "hi")
			if err != nil {
				t.Fatal(err)
			}
			if res.StopReason != tc.want {
				t.Errorf("StopReason = %q, want %q", res.StopReason, tc.want)
			}
			if res.Text != "partial" {
				t.Errorf("Text = %q, want partial answer kept", res.Text)
			}
		})
	}
}

func TestModelEndLifecycleUsesProviderStopReason(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "cut", StopReason: ModelStopMaxTokens})
	a, err := New(sp, "sys")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for ev, err := range a.Stream(Background().WithDetailedEvents(), "hi") {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type == EventModelEnd && ev.Lifecycle != nil {
			got = ev.Lifecycle.StopReason
		}
	}
	if got != string(ModelStopMaxTokens) {
		t.Fatalf("model_end StopReason = %q, want %q", got, ModelStopMaxTokens)
	}
}

func TestModelStopReasonFallsBackToDerived(t *testing.T) {
	if got := modelStopReason(&ModelResponse{}, nil); got != StopReasonEndTurn {
		t.Errorf("no reason, no tools = %q", got)
	}
	if got := modelStopReason(&ModelResponse{StopReason: ModelStopEndTurn}, errTest); got != StopReasonError {
		t.Errorf("error must win over reported reason, got %q", got)
	}
}

var errTest = testError("boom")

type testError string

func (e testError) Error() string { return string(e) }
