package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// genOptionalFloat generates a random optional float64 pointer.
// Roughly half the time it returns nil (field not set).
func genOptionalFloat(t *rapid.T, name string) *float64 {
	if rapid.Bool().Draw(t, name+"_present") {
		v := rapid.Float64Range(-2.0, 3.0).Draw(t, name)
		return &v
	}
	return nil
}

// genOptionalInt generates a random optional int pointer.
// Roughly half the time it returns nil (field not set).
func genOptionalInt(t *rapid.T, name string) *int {
	if rapid.Bool().Draw(t, name+"_present") {
		v := rapid.IntRange(-10, 1000).Draw(t, name)
		return &v
	}
	return nil
}

// genOptionalStringSlice generates a random optional string slice.
// Roughly half the time it returns nil (field not set).
func genOptionalStringSlice(t *rapid.T, name string) []string {
	if rapid.Bool().Draw(t, name+"_present") {
		n := rapid.IntRange(0, 5).Draw(t, name+"_len")
		s := make([]string, n)
		for i := range n {
			s[i] = rapid.StringMatching(`[a-z]{1,8}`).Draw(t, name+"_elem")
		}
		return s
	}
	return nil
}

// genInferenceConfig generates a random InferenceConfig with randomly nil/non-nil fields.
func genInferenceConfig(t *rapid.T) *InferenceConfig {
	return &InferenceConfig{
		Temperature:   genOptionalFloat(t, "temperature"),
		TopP:          genOptionalFloat(t, "topP"),
		TopK:          genOptionalInt(t, "topK"),
		StopSequences: genOptionalStringSlice(t, "stopSeqs"),
		MaxTokens:     genOptionalInt(t, "maxTokens"),
	}
}

// ---------------------------------------------------------------------------
// Property 1: InferenceConfig Context Round-Trip
// ---------------------------------------------------------------------------

// Feature: inference-parameters, Property 1: InferenceConfig Context Round-Trip
//
// TestProperty_InferenceConfigContextRoundTrip verifies that for any valid
// InferenceConfig, attaching it to a context via WithInferenceConfig and then
// retrieving it via GetInferenceConfig returns an equivalent InferenceConfig.
//
// **Validates: Requirements 4.1**
func TestProperty_InferenceConfigContextRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cfg := genInferenceConfig(rt)

		ctx := WithInferenceConfig(context.Background(), cfg)
		got := GetInferenceConfig(ctx)

		if !reflect.DeepEqual(cfg, got) {
			rt.Fatalf("round-trip mismatch:\nput: %+v\ngot: %+v", cfg, got)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: Merge Precedence
// ---------------------------------------------------------------------------

// Feature: inference-parameters, Property 2: Merge Precedence
//
// TestProperty_MergePrecedence verifies that for any two InferenceConfig values
// (agent-level and per-invocation), merging them produces a result where: for
// each field, if the per-invocation value is non-nil, the result equals the
// per-invocation value; otherwise the result equals the agent-level value.
//
// **Validates: Requirements 4.2, 4.3**
func TestProperty_MergePrecedence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		agentCfg := genInferenceConfig(rt)
		perCfg := genInferenceConfig(rt)

		merged := mergeInferenceConfig(agentCfg, perCfg)

		if merged == nil {
			rt.Fatal("merged should not be nil when both inputs are non-nil")
		}

		// Temperature: per-invocation wins if non-nil, else agent-level
		if perCfg.Temperature != nil {
			if merged.Temperature == nil || *merged.Temperature != *perCfg.Temperature {
				rt.Fatalf("Temperature: expected per-invocation %v, got %v", perCfg.Temperature, merged.Temperature)
			}
		} else {
			if !reflect.DeepEqual(merged.Temperature, agentCfg.Temperature) {
				rt.Fatalf("Temperature: expected agent-level %v, got %v", agentCfg.Temperature, merged.Temperature)
			}
		}

		// TopP: per-invocation wins if non-nil, else agent-level
		if perCfg.TopP != nil {
			if merged.TopP == nil || *merged.TopP != *perCfg.TopP {
				rt.Fatalf("TopP: expected per-invocation %v, got %v", perCfg.TopP, merged.TopP)
			}
		} else {
			if !reflect.DeepEqual(merged.TopP, agentCfg.TopP) {
				rt.Fatalf("TopP: expected agent-level %v, got %v", agentCfg.TopP, merged.TopP)
			}
		}

		// TopK: per-invocation wins if non-nil, else agent-level
		if perCfg.TopK != nil {
			if merged.TopK == nil || *merged.TopK != *perCfg.TopK {
				rt.Fatalf("TopK: expected per-invocation %v, got %v", perCfg.TopK, merged.TopK)
			}
		} else {
			if !reflect.DeepEqual(merged.TopK, agentCfg.TopK) {
				rt.Fatalf("TopK: expected agent-level %v, got %v", agentCfg.TopK, merged.TopK)
			}
		}

		// StopSequences: per-invocation wins if non-nil, else agent-level
		if perCfg.StopSequences != nil {
			if !reflect.DeepEqual(merged.StopSequences, perCfg.StopSequences) {
				rt.Fatalf("StopSequences: expected per-invocation %v, got %v", perCfg.StopSequences, merged.StopSequences)
			}
		} else {
			if !reflect.DeepEqual(merged.StopSequences, agentCfg.StopSequences) {
				rt.Fatalf("StopSequences: expected agent-level %v, got %v", agentCfg.StopSequences, merged.StopSequences)
			}
		}

		// MaxTokens: per-invocation wins if non-nil, else agent-level
		if perCfg.MaxTokens != nil {
			if merged.MaxTokens == nil || *merged.MaxTokens != *perCfg.MaxTokens {
				rt.Fatalf("MaxTokens: expected per-invocation %v, got %v", perCfg.MaxTokens, merged.MaxTokens)
			}
		} else {
			if !reflect.DeepEqual(merged.MaxTokens, agentCfg.MaxTokens) {
				rt.Fatalf("MaxTokens: expected agent-level %v, got %v", agentCfg.MaxTokens, merged.MaxTokens)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Float Parameter Validation
// ---------------------------------------------------------------------------

// Feature: inference-parameters, Property 3: Float Parameter Validation
//
// TestProperty_FloatParameterValidation verifies that for any float64 value,
// WithTemperature and WithTopP return an error if and only if the value is
// outside the range [0.0, 1.0].
//
// **Validates: Requirements 9.1, 9.2**
func TestProperty_FloatParameterValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		v := rapid.Float64Range(-10.0, 10.0).Draw(rt, "value")
		inRange := v >= 0.0 && v <= 1.0

		// Test WithTemperature
		tempOpt := WithTemperature(v)
		tempErr := tempOpt(&Agent{})
		if inRange && tempErr != nil {
			rt.Fatalf("WithTemperature(%f): expected no error for in-range value, got %v", v, tempErr)
		}
		if !inRange && tempErr == nil {
			rt.Fatalf("WithTemperature(%f): expected error for out-of-range value, got nil", v)
		}

		// Test WithTopP
		topPOpt := WithTopP(v)
		topPErr := topPOpt(&Agent{})
		if inRange && topPErr != nil {
			rt.Fatalf("WithTopP(%f): expected no error for in-range value, got %v", v, topPErr)
		}
		if !inRange && topPErr == nil {
			rt.Fatalf("WithTopP(%f): expected error for out-of-range value, got nil", v)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: Integer Parameter Validation
// ---------------------------------------------------------------------------

// Feature: inference-parameters, Property 4: Integer Parameter Validation
//
// TestProperty_IntegerParameterValidation verifies that for any integer value,
// WithTopK returns an error if and only if the value is less than 1. Similarly,
// validateInferenceConfig rejects an InferenceConfig whose MaxTokens is less
// than 1.
//
// **Validates: Requirements 9.3, 9.4**
func TestProperty_IntegerParameterValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		v := rapid.IntRange(-100, 1000).Draw(rt, "value")
		valid := v >= 1

		// Test WithTopK
		topKOpt := WithTopK(v)
		topKErr := topKOpt(&Agent{})
		if valid && topKErr != nil {
			rt.Fatalf("WithTopK(%d): expected no error for valid value, got %v", v, topKErr)
		}
		if !valid && topKErr == nil {
			rt.Fatalf("WithTopK(%d): expected error for invalid value, got nil", v)
		}

		// Test validateInferenceConfig with MaxTokens
		cfg := &InferenceConfig{MaxTokens: &v}
		valErr := validateInferenceConfig(cfg)
		if valid && valErr != nil {
			rt.Fatalf("validateInferenceConfig(MaxTokens=%d): expected no error for valid value, got %v", v, valErr)
		}
		if !valid && valErr == nil {
			rt.Fatalf("validateInferenceConfig(MaxTokens=%d): expected error for invalid value, got nil", v)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Per-Invocation Validation Blocks Provider Call
// ---------------------------------------------------------------------------

// noCallProvider is a mock provider that records whether it was called.
// Used by Property 5 to verify the provider is never invoked when
// per-invocation validation fails.
type noCallProvider struct {
	called bool
}

func (p *noCallProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	p.called = true
	return &ProviderResponse{Text: "should not reach"}, nil
}

func (p *noCallProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	p.called = true
	return &ProviderResponse{Text: "should not reach"}, nil
}

// genInvalidInferenceConfig generates an InferenceConfig with at least one
// invalid field: Temperature outside [0.0, 1.0], TopP outside [0.0, 1.0],
// TopK < 1, or MaxTokens < 1.
func genInvalidInferenceConfig(t *rapid.T) *InferenceConfig {
	cfg := &InferenceConfig{}

	// Pick which fields to make invalid (at least one).
	// Use a bitmask: bit 0 = temperature, bit 1 = topP, bit 2 = topK, bit 3 = maxTokens
	mask := rapid.IntRange(1, 15).Draw(t, "invalidMask") // 1..15 ensures at least one bit set

	if mask&1 != 0 {
		// Invalid temperature: either < 0 or > 1
		if rapid.Bool().Draw(t, "tempNeg") {
			v := rapid.Float64Range(-10.0, -0.001).Draw(t, "badTemp")
			cfg.Temperature = &v
		} else {
			v := rapid.Float64Range(1.001, 10.0).Draw(t, "badTemp")
			cfg.Temperature = &v
		}
	}

	if mask&2 != 0 {
		// Invalid topP: either < 0 or > 1
		if rapid.Bool().Draw(t, "topPNeg") {
			v := rapid.Float64Range(-10.0, -0.001).Draw(t, "badTopP")
			cfg.TopP = &v
		} else {
			v := rapid.Float64Range(1.001, 10.0).Draw(t, "badTopP")
			cfg.TopP = &v
		}
	}

	if mask&4 != 0 {
		// Invalid topK: < 1
		v := rapid.IntRange(-100, 0).Draw(t, "badTopK")
		cfg.TopK = &v
	}

	if mask&8 != 0 {
		// Invalid maxTokens: < 1
		v := rapid.IntRange(-100, 0).Draw(t, "badMaxTokens")
		cfg.MaxTokens = &v
	}

	return cfg
}

// Feature: inference-parameters, Property 5: Per-Invocation Validation Blocks Provider Call
//
// TestProperty_PerInvocationValidationBlocksProviderCall verifies that for any
// InferenceConfig containing at least one invalid field, when set as a
// per-invocation override, the agent returns a validation error without
// invoking the provider.
//
// **Validates: Requirements 9.5**
func TestProperty_PerInvocationValidationBlocksProviderCall(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		invalidCfg := genInvalidInferenceConfig(rt)
		mock := &noCallProvider{}

		a, err := New(mock, prompt.Text("sys"), nil)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		ctx := WithInferenceConfig(context.Background(), invalidCfg)
		_, _, invokeErr := a.Invoke(ctx, "hello")

		if invokeErr == nil {
			rt.Fatalf("expected validation error for invalid config %+v, got nil", invalidCfg)
		}

		if mock.called {
			rt.Fatalf("provider was called despite invalid per-invocation config %+v", invalidCfg)
		}
	})
}
