package agent

import "testing"

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
