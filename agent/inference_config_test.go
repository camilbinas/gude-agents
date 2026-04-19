package agent

import (
	"testing"
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
