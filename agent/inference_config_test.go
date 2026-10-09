package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// helpers to create pointer values
func ptrFloat(v float64) *float64 { return &v }
func ptrInt(v int) *int           { return &v }

// ---------------------------------------------------------------------------
// mergeInferenceConfig tests
// ---------------------------------------------------------------------------

func TestMergeInferenceConfig_NilNil(t *testing.T) {
	got := mergeInferenceConfig(nil, nil)
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestMergeInferenceConfig_NonNilNil(t *testing.T) {
	agent := &InferenceConfig{
		Temperature: ptrFloat(0.5),
		TopK:        ptrInt(10),
	}
	got := mergeInferenceConfig(agent, nil)
	if got != agent {
		t.Fatal("expected agent-level config returned as-is")
	}
}

func TestMergeInferenceConfig_NilNonNil(t *testing.T) {
	per := &InferenceConfig{
		TopP:      ptrFloat(0.9),
		MaxTokens: ptrInt(100),
	}
	got := mergeInferenceConfig(nil, per)
	if got != per {
		t.Fatal("expected per-invocation config returned as-is")
	}
}

func TestMergeInferenceConfig_FieldByField(t *testing.T) {
	agent := &InferenceConfig{
		Temperature:   ptrFloat(0.5),
		TopP:          ptrFloat(0.8),
		TopK:          ptrInt(10),
		StopSequences: []string{"stop1"},
		MaxTokens:     ptrInt(200),
	}
	per := &InferenceConfig{
		Temperature: ptrFloat(0.9),
		// TopP nil — should fall back to agent
		TopK: ptrInt(20),
		// StopSequences nil — should fall back to agent
		MaxTokens: ptrInt(500),
	}

	got := mergeInferenceConfig(agent, per)

	if got == nil {
		t.Fatal("expected non-nil merged config")
	}
	if *got.Temperature != 0.9 {
		t.Errorf("Temperature: expected 0.9, got %f", *got.Temperature)
	}
	if *got.TopP != 0.8 {
		t.Errorf("TopP: expected 0.8 (agent fallback), got %f", *got.TopP)
	}
	if *got.TopK != 20 {
		t.Errorf("TopK: expected 20, got %d", *got.TopK)
	}
	if len(got.StopSequences) != 1 || got.StopSequences[0] != "stop1" {
		t.Errorf("StopSequences: expected [stop1] (agent fallback), got %v", got.StopSequences)
	}
	if *got.MaxTokens != 500 {
		t.Errorf("MaxTokens: expected 500, got %d", *got.MaxTokens)
	}
}

func TestMergeInferenceConfig_PerInvocationEmptySliceOverridesAgentSlice(t *testing.T) {
	agent := &InferenceConfig{
		StopSequences: []string{"stop1", "stop2"},
	}
	per := &InferenceConfig{
		StopSequences: []string{}, // non-nil empty slice is a valid override
	}

	got := mergeInferenceConfig(agent, per)

	if got.StopSequences == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(got.StopSequences) != 0 {
		t.Errorf("expected empty slice, got %v", got.StopSequences)
	}
}

func TestMergeInferenceConfig_AllFieldsFromPerInvocation(t *testing.T) {
	agent := &InferenceConfig{
		Temperature:   ptrFloat(0.1),
		TopP:          ptrFloat(0.2),
		TopK:          ptrInt(5),
		StopSequences: []string{"a"},
		MaxTokens:     ptrInt(50),
	}
	per := &InferenceConfig{
		Temperature:   ptrFloat(0.9),
		TopP:          ptrFloat(0.8),
		TopK:          ptrInt(40),
		StopSequences: []string{"b", "c"},
		MaxTokens:     ptrInt(1000),
	}

	got := mergeInferenceConfig(agent, per)

	if *got.Temperature != 0.9 {
		t.Errorf("Temperature: expected 0.9, got %f", *got.Temperature)
	}
	if *got.TopP != 0.8 {
		t.Errorf("TopP: expected 0.8, got %f", *got.TopP)
	}
	if *got.TopK != 40 {
		t.Errorf("TopK: expected 40, got %d", *got.TopK)
	}
	if len(got.StopSequences) != 2 || got.StopSequences[0] != "b" {
		t.Errorf("StopSequences: expected [b c], got %v", got.StopSequences)
	}
	if *got.MaxTokens != 1000 {
		t.Errorf("MaxTokens: expected 1000, got %d", *got.MaxTokens)
	}
}

// ---------------------------------------------------------------------------
// validateInferenceConfig tests
// ---------------------------------------------------------------------------

func TestValidateInferenceConfig_Nil(t *testing.T) {
	if err := validateInferenceConfig(nil); err != nil {
		t.Fatalf("expected nil error for nil config, got %v", err)
	}
}

func TestValidateInferenceConfig_AllValid(t *testing.T) {
	cfg := &InferenceConfig{
		Temperature:   ptrFloat(0.7),
		TopP:          ptrFloat(0.9),
		TopK:          ptrInt(50),
		StopSequences: []string{"end"},
		MaxTokens:     ptrInt(1024),
	}
	if err := validateInferenceConfig(cfg); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateInferenceConfig_BoundaryValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  *InferenceConfig
	}{
		{"temperature=0.0", &InferenceConfig{Temperature: ptrFloat(0.0)}},
		{"temperature=1.0", &InferenceConfig{Temperature: ptrFloat(1.0)}},
		{"topP=0.0", &InferenceConfig{TopP: ptrFloat(0.0)}},
		{"topP=1.0", &InferenceConfig{TopP: ptrFloat(1.0)}},
		{"topK=1", &InferenceConfig{TopK: ptrInt(1)}},
		{"maxTokens=1", &InferenceConfig{MaxTokens: ptrInt(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateInferenceConfig(tt.cfg); err != nil {
				t.Errorf("expected no error for boundary value, got %v", err)
			}
		})
	}
}

func TestValidateInferenceConfig_InvalidTemperature(t *testing.T) {
	tests := []struct {
		name string
		val  float64
	}{
		{"negative", -0.1},
		{"above_one", 1.1},
		{"large", 5.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &InferenceConfig{Temperature: ptrFloat(tt.val)}
			if err := validateInferenceConfig(cfg); err == nil {
				t.Errorf("expected error for temperature=%f, got nil", tt.val)
			}
		})
	}
}

func TestValidateInferenceConfig_InvalidTopP(t *testing.T) {
	tests := []struct {
		name string
		val  float64
	}{
		{"negative", -0.5},
		{"above_one", 1.01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &InferenceConfig{TopP: ptrFloat(tt.val)}
			if err := validateInferenceConfig(cfg); err == nil {
				t.Errorf("expected error for topP=%f, got nil", tt.val)
			}
		})
	}
}

func TestValidateInferenceConfig_InvalidTopK(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"zero", 0},
		{"negative", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &InferenceConfig{TopK: ptrInt(tt.val)}
			if err := validateInferenceConfig(cfg); err == nil {
				t.Errorf("expected error for topK=%d, got nil", tt.val)
			}
		})
	}
}

func TestValidateInferenceConfig_InvalidMaxTokens(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"zero", 0},
		{"negative", -10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &InferenceConfig{MaxTokens: ptrInt(tt.val)}
			if err := validateInferenceConfig(cfg); err == nil {
				t.Errorf("expected error for maxTokens=%d, got nil", tt.val)
			}
		})
	}
}

func TestValidateInferenceConfig_AllFieldsNil(t *testing.T) {
	cfg := &InferenceConfig{}
	if err := validateInferenceConfig(cfg); err != nil {
		t.Fatalf("expected no error for empty config, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// WithTemperature tests
// ---------------------------------------------------------------------------

func TestWithTemperature_Valid(t *testing.T) {
	tests := []struct {
		name string
		val  float64
	}{
		{"zero", 0.0},
		{"mid", 0.5},
		{"one", 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithTemperature(tt.val)
			if err := opt(a); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.inferenceConfig == nil {
				t.Fatal("expected inferenceConfig to be initialized")
			}
			if a.inferenceConfig.Temperature == nil {
				t.Fatal("expected Temperature to be set")
			}
			if *a.inferenceConfig.Temperature != tt.val {
				t.Errorf("expected %f, got %f", tt.val, *a.inferenceConfig.Temperature)
			}
		})
	}
}

func TestWithTemperature_Invalid(t *testing.T) {
	tests := []struct {
		name string
		val  float64
	}{
		{"negative", -0.1},
		{"above_one", 1.1},
		{"large", 5.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithTemperature(tt.val)
			if err := opt(a); err == nil {
				t.Errorf("expected error for temperature=%f, got nil", tt.val)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WithTopP tests
// ---------------------------------------------------------------------------

func TestWithTopP_Valid(t *testing.T) {
	tests := []struct {
		name string
		val  float64
	}{
		{"zero", 0.0},
		{"mid", 0.7},
		{"one", 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithTopP(tt.val)
			if err := opt(a); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.inferenceConfig == nil {
				t.Fatal("expected inferenceConfig to be initialized")
			}
			if a.inferenceConfig.TopP == nil {
				t.Fatal("expected TopP to be set")
			}
			if *a.inferenceConfig.TopP != tt.val {
				t.Errorf("expected %f, got %f", tt.val, *a.inferenceConfig.TopP)
			}
		})
	}
}

func TestWithTopP_Invalid(t *testing.T) {
	tests := []struct {
		name string
		val  float64
	}{
		{"negative", -0.5},
		{"above_one", 1.01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithTopP(tt.val)
			if err := opt(a); err == nil {
				t.Errorf("expected error for topP=%f, got nil", tt.val)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WithTopK tests
// ---------------------------------------------------------------------------

func TestWithTopK_Valid(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"one", 1},
		{"ten", 10},
		{"large", 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithTopK(tt.val)
			if err := opt(a); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.inferenceConfig == nil {
				t.Fatal("expected inferenceConfig to be initialized")
			}
			if a.inferenceConfig.TopK == nil {
				t.Fatal("expected TopK to be set")
			}
			if *a.inferenceConfig.TopK != tt.val {
				t.Errorf("expected %d, got %d", tt.val, *a.inferenceConfig.TopK)
			}
		})
	}
}

func TestWithTopK_Invalid(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"zero", 0},
		{"negative", -1},
		{"very_negative", -100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithTopK(tt.val)
			if err := opt(a); err == nil {
				t.Errorf("expected error for topK=%d, got nil", tt.val)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WithStopSequences tests
// ---------------------------------------------------------------------------

func TestWithStopSequences_Valid(t *testing.T) {
	a := &Agent{}
	seqs := []string{"stop1", "stop2"}
	opt := WithStopSequences(seqs)
	if err := opt(a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.inferenceConfig == nil {
		t.Fatal("expected inferenceConfig to be initialized")
	}
	if len(a.inferenceConfig.StopSequences) != 2 {
		t.Fatalf("expected 2 stop sequences, got %d", len(a.inferenceConfig.StopSequences))
	}
	if a.inferenceConfig.StopSequences[0] != "stop1" || a.inferenceConfig.StopSequences[1] != "stop2" {
		t.Errorf("unexpected stop sequences: %v", a.inferenceConfig.StopSequences)
	}
}

func TestWithStopSequences_EmptySlice(t *testing.T) {
	a := &Agent{}
	opt := WithStopSequences([]string{})
	if err := opt(a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.inferenceConfig == nil {
		t.Fatal("expected inferenceConfig to be initialized")
	}
	if a.inferenceConfig.StopSequences == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(a.inferenceConfig.StopSequences) != 0 {
		t.Errorf("expected empty slice, got %v", a.inferenceConfig.StopSequences)
	}
}

func TestWithStopSequences_NilSlice(t *testing.T) {
	a := &Agent{}
	opt := WithStopSequences(nil)
	if err := opt(a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.inferenceConfig == nil {
		t.Fatal("expected inferenceConfig to be initialized")
	}
}

// ---------------------------------------------------------------------------
// Lazy initialization: inferenceConfig is created only when needed
// ---------------------------------------------------------------------------

func TestInferenceOptions_LazyInit(t *testing.T) {
	a := &Agent{}
	if a.inferenceConfig != nil {
		t.Fatal("expected inferenceConfig to be nil before any option is applied")
	}

	opt := WithTemperature(0.5)
	if err := opt(a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.inferenceConfig == nil {
		t.Fatal("expected inferenceConfig to be initialized after WithTemperature")
	}
}

// ---------------------------------------------------------------------------
// Multiple options compose correctly
// ---------------------------------------------------------------------------

func TestInferenceOptions_Compose(t *testing.T) {
	a := &Agent{}
	opts := []Option{
		WithTemperature(0.8),
		WithTopP(0.9),
		WithTopK(50),
		WithStopSequences([]string{"END"}),
	}
	for _, opt := range opts {
		if err := opt(a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	cfg := a.inferenceConfig
	if cfg == nil {
		t.Fatal("expected inferenceConfig to be set")
	}
	if *cfg.Temperature != 0.8 {
		t.Errorf("Temperature: expected 0.8, got %f", *cfg.Temperature)
	}
	if *cfg.TopP != 0.9 {
		t.Errorf("TopP: expected 0.9, got %f", *cfg.TopP)
	}
	if *cfg.TopK != 50 {
		t.Errorf("TopK: expected 50, got %d", *cfg.TopK)
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "END" {
		t.Errorf("StopSequences: expected [END], got %v", cfg.StopSequences)
	}
}

// ---------------------------------------------------------------------------
// WithMaxOutputTokens tests
// ---------------------------------------------------------------------------

func TestWithMaxOutputTokens_Valid(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"one", 1},
		{"typical", 4096},
		{"large", 100000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithMaxOutputTokens(tt.val)
			if err := opt(a); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.inferenceConfig == nil {
				t.Fatal("expected inferenceConfig to be initialized")
			}
			if a.inferenceConfig.MaxTokens == nil {
				t.Fatal("expected MaxTokens to be set")
			}
			if *a.inferenceConfig.MaxTokens != tt.val {
				t.Errorf("expected %d, got %d", tt.val, *a.inferenceConfig.MaxTokens)
			}
		})
	}
}

func TestWithMaxOutputTokens_Invalid(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"zero", 0},
		{"negative", -1},
		{"very_negative", -100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			opt := WithMaxOutputTokens(tt.val)
			if err := opt(a); err == nil {
				t.Errorf("expected error for maxTokens=%d, got nil", tt.val)
			}
		})
	}
}

func TestInvoke_AgentLevelInferenceConfigForwarded(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "ok"})

	a, err := New(cp, "sys",
		WithTemperature(0.7),
		WithTopP(0.9),
		WithTopK(50),
		WithStopSequences([]string{"STOP"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}

	cfg := cp.captured[0].InferenceConfig
	if cfg == nil {
		t.Fatal("expected InferenceConfig to be set, got nil")
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("Temperature: expected 0.7, got %v", cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != 0.9 {
		t.Errorf("TopP: expected 0.9, got %v", cfg.TopP)
	}
	if cfg.TopK == nil || *cfg.TopK != 50 {
		t.Errorf("TopK: expected 50, got %v", cfg.TopK)
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences: expected [STOP], got %v", cfg.StopSequences)
	}
}

func TestInvoke_PerInvocationOverridesAgentLevel(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "ok"})

	a, err := New(cp, "sys",
		WithTemperature(0.3),
		WithTopP(0.5),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Per-invocation override: temperature=0.9, leave TopP to fall back to agent-level.
	ctx := Background().WithInferenceConfig(&InferenceConfig{
		Temperature: ptrFloat(0.9),
	})

	_, err = a.Invoke(ctx, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}

	cfg := cp.captured[0].InferenceConfig
	if cfg == nil {
		t.Fatal("expected InferenceConfig to be set, got nil")
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.9 {
		t.Errorf("Temperature: expected 0.9 (per-invocation), got %v", cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != 0.5 {
		t.Errorf("TopP: expected 0.5 (agent-level fallback), got %v", cfg.TopP)
	}
}

func TestInvoke_NilInferenceConfigWhenNoneSet(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "ok"})

	a, err := New(cp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}

	if cp.captured[0].InferenceConfig != nil {
		t.Errorf("expected InferenceConfig to be nil, got %+v", cp.captured[0].InferenceConfig)
	}
}

func TestInvoke_InvalidPerInvocationConfigReturnsError(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "should not reach"})

	a, err := New(cp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	// Per-invocation config with invalid temperature.
	ctx := Background().WithInferenceConfig(&InferenceConfig{
		Temperature: ptrFloat(2.0), // invalid: > 1.0
	})

	_, err = a.Invoke(ctx, "hello")
	if err == nil {
		t.Fatal("expected error for invalid per-invocation config, got nil")
	}
	if !strings.Contains(err.Error(), "inference config") {
		t.Errorf("expected error to mention 'inference config', got: %v", err)
	}

	// Provider should NOT have been called.
	if len(cp.captured) != 0 {
		t.Errorf("expected 0 provider calls (validation should block), got %d", len(cp.captured))
	}
}

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

// TestProperty_InferenceConfigContextRoundTrip verifies that for any valid
// InferenceConfig, attaching it to a context via WithInferenceConfig and then
// retrieving it via GetInferenceConfig returns an equivalent InferenceConfig.
func TestProperty_InferenceConfigContextRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cfg := genInferenceConfig(rt)

		c := Background().WithInferenceConfig(cfg)
		got := c.InferenceConfig()

		if !reflect.DeepEqual(cfg, got) {
			rt.Fatalf("round-trip mismatch:\nput: %+v\ngot: %+v", cfg, got)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: Merge Precedence
// ---------------------------------------------------------------------------

// TestProperty_MergePrecedence verifies that for any two InferenceConfig values
// (agent-level and per-invocation), merging them produces a result where: for
// each field, if the per-invocation value is non-nil, the result equals the
// per-invocation value; otherwise the result equals the agent-level value.
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

// TestProperty_FloatParameterValidation verifies that for any float64 value,
// WithTemperature and WithTopP return an error if and only if the value is
// outside the range [0.0, 1.0].
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

// TestProperty_IntegerParameterValidation verifies that for any integer value,
// WithTopK returns an error if and only if the value is less than 1. Similarly,
// validateInferenceConfig rejects an InferenceConfig whose MaxTokens is less
// than 1.
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

func (p *noCallProvider) Name() string { return "mock" }

func (p *noCallProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	p.called = true
	return &ModelResponse{Text: "should not reach"}, nil
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

// TestProperty_PerInvocationValidationBlocksProviderCall verifies that for any
// InferenceConfig containing at least one invalid field, when set as a
// per-invocation override, the agent returns a validation error without
// invoking the provider.
func TestProperty_PerInvocationValidationBlocksProviderCall(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		invalidCfg := genInvalidInferenceConfig(rt)
		mock := &noCallProvider{}

		a, err := New(mock, "sys")
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		ctx := Background().WithInferenceConfig(invalidCfg)
		_, invokeErr := a.Invoke(ctx, "hello")

		if invokeErr == nil {
			rt.Fatalf("expected validation error for invalid config %+v, got nil", invalidCfg)
		}

		if mock.called {
			rt.Fatalf("provider was called despite invalid per-invocation config %+v", invalidCfg)
		}
	})
}
