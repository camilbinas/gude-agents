package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// JSONStructure is a rule-based evaluator that validates whether the actual
// output is valid JSON and optionally checks for required top-level keys.
// Score is 1.0 if valid JSON with all required keys, 0.0 otherwise.
type JSONStructure struct {
	requiredKeys []string
	cfg          evaluatorConfig
}

// NewJSONStructure creates a JSONStructure evaluator that checks for valid JSON
// and the presence of the given top-level keys. An empty requiredKeys slice is
// valid and means only JSON validity is checked.
func NewJSONStructure(requiredKeys []string, opts ...EvaluatorOption) *JSONStructure {
	cfg := applyOptions(opts)
	return &JSONStructure{
		requiredKeys: requiredKeys,
		cfg:          cfg,
	}
}

// Evaluate checks whether the actual output is valid JSON and contains all
// required top-level keys. Returns score 1.0 on success, 0.0 with a
// descriptive Explanation on failure.
func (j *JSONStructure) Evaluate(_ context.Context, ec EvalCase) (EvalResult, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(ec.ActualOutput), &parsed); err != nil {
		return EvalResult{
			EvaluatorName: j.Name(),
			Score:         0.0,
			Pass:          applyThreshold(0.0, j.cfg),
			Explanation:   fmt.Sprintf("invalid JSON: %s", err.Error()),
		}, nil
	}

	var missing []string
	for _, key := range j.requiredKeys {
		if _, ok := parsed[key]; !ok {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return EvalResult{
			EvaluatorName: j.Name(),
			Score:         0.0,
			Pass:          applyThreshold(0.0, j.cfg),
			Explanation:   fmt.Sprintf("missing required keys: %s", strings.Join(missing, ", ")),
		}, nil
	}

	return EvalResult{
		EvaluatorName: j.Name(),
		Score:         1.0,
		Pass:          applyThreshold(1.0, j.cfg),
	}, nil
}

// Name returns the evaluator name.
func (j *JSONStructure) Name() string {
	return "json_structure"
}
