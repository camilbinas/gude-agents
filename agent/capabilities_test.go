package agent

import (
	"context"
	"testing"
)

type capabilityTestProvider struct {
	caps ModelCapabilities
}

func (capabilityTestProvider) Name() string { return "capability-test" }
func (capabilityTestProvider) Stream(context.Context, ModelRequest, func(ModelEvent)) (*ModelResponse, error) {
	return &ModelResponse{}, nil
}
func (p capabilityTestProvider) Capabilities() ModelCapabilities { return p.caps }

type plainProvider struct{}

func (plainProvider) Name() string { return "plain" }
func (plainProvider) Stream(context.Context, ModelRequest, func(ModelEvent)) (*ModelResponse, error) {
	return &ModelResponse{}, nil
}

func TestCapabilitiesOf(t *testing.T) {
	want := ModelCapabilities{
		ContextWindowTokens:    200000,
		MaxOutputTokens:        16000,
		ToolUse:                Supported,
		NativeStructuredOutput: Unsupported,
		ToolChoice:             ToolChoiceCapabilities{Auto: Supported, Required: Supported, Specific: Unsupported},
	}

	if got := CapabilitiesOf(capabilityTestProvider{caps: want}); got != want {
		t.Fatalf("CapabilitiesOf(capability provider) = %#v, want %#v", got, want)
	}
	if got := CapabilitiesOf(plainProvider{}); got != (ModelCapabilities{}) {
		t.Fatalf("CapabilitiesOf(plain provider) = %#v, want unknown zero value", got)
	}
	if got := CapabilitiesOf(nil); got != (ModelCapabilities{}) {
		t.Fatalf("CapabilitiesOf(nil) = %#v, want unknown zero value", got)
	}
}

func TestMergeModelCapabilitiesPreservesUnspecifiedFields(t *testing.T) {
	base := ModelCapabilities{
		ContextWindowTokens:    200000,
		MaxOutputTokens:        16000,
		ToolUse:                Supported,
		NativeStructuredOutput: Unsupported,
		ToolChoice:             ToolChoiceCapabilities{Auto: Supported, Required: Supported, Specific: Unsupported},
	}
	got := MergeModelCapabilities(base, ModelCapabilities{
		ToolChoice: ToolChoiceCapabilities{Specific: Supported},
	})
	want := base
	want.ToolChoice.Specific = Supported
	if got != want {
		t.Fatalf("MergeModelCapabilities() = %#v, want %#v", got, want)
	}
}

var _ Provider = plainProvider{}
var _ Provider = capabilityTestProvider{}
var _ CapabilityProvider = capabilityTestProvider{}
