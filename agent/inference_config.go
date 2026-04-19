package agent

import "fmt"

// mergeInferenceConfig merges per-invocation config over agent-level config.
// Per-invocation non-nil fields take precedence. Returns nil if both inputs are nil.
func mergeInferenceConfig(agentLevel, perInvocation *InferenceConfig) *InferenceConfig {
	if agentLevel == nil && perInvocation == nil {
		return nil
	}
	if agentLevel == nil {
		return perInvocation
	}
	if perInvocation == nil {
		return agentLevel
	}

	merged := &InferenceConfig{
		Temperature:   agentLevel.Temperature,
		TopP:          agentLevel.TopP,
		TopK:          agentLevel.TopK,
		StopSequences: agentLevel.StopSequences,
		MaxTokens:     agentLevel.MaxTokens,
	}

	if perInvocation.Temperature != nil {
		merged.Temperature = perInvocation.Temperature
	}
	if perInvocation.TopP != nil {
		merged.TopP = perInvocation.TopP
	}
	if perInvocation.TopK != nil {
		merged.TopK = perInvocation.TopK
	}
	if perInvocation.StopSequences != nil {
		merged.StopSequences = perInvocation.StopSequences
	}
	if perInvocation.MaxTokens != nil {
		merged.MaxTokens = perInvocation.MaxTokens
	}

	return merged
}

// validateInferenceConfig checks that all set fields are within valid ranges.
// Returns nil if cfg is nil.
func validateInferenceConfig(cfg *InferenceConfig) error {
	if cfg == nil {
		return nil
	}

	if cfg.Temperature != nil {
		if *cfg.Temperature < 0.0 || *cfg.Temperature > 1.0 {
			return fmt.Errorf("temperature must be between 0.0 and 1.0, got %f", *cfg.Temperature)
		}
	}
	if cfg.TopP != nil {
		if *cfg.TopP < 0.0 || *cfg.TopP > 1.0 {
			return fmt.Errorf("top_p must be between 0.0 and 1.0, got %f", *cfg.TopP)
		}
	}
	if cfg.TopK != nil {
		if *cfg.TopK < 1 {
			return fmt.Errorf("top_k must be >= 1, got %d", *cfg.TopK)
		}
	}
	if cfg.MaxTokens != nil {
		if *cfg.MaxTokens < 1 {
			return fmt.Errorf("max_tokens must be >= 1, got %d", *cfg.MaxTokens)
		}
	}

	return nil
}
