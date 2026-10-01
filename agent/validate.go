package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// supportedSchemaKeywords lists the JSON Schema keywords ValidateToolInput
// enforces. Keep it in sync with validateValue, the ValidateToolInput doc
// comment, and docs/structured-output.md; validate_contract_test.go checks
// that this list matches observed behavior.
var supportedSchemaKeywords = []string{"type", "enum", "required", "properties", "items"}

// ValidateToolInput checks a JSON payload against the framework's supported
// JSON Schema subset. It is not a general JSON Schema validator.
//
// Enforced, recursively through nested objects and arrays:
//   - the payload is valid JSON;
//   - "type", including union arrays such as ["string", "null"];
//   - "enum";
//   - "required";
//   - "properties" (each present property against its subschema);
//   - "items" (each element against a single item schema).
//
// Any other keyword (for example minimum, maxLength, pattern,
// additionalProperties, oneOf, $ref) is not enforced: it is passed to the
// model as guidance only. Callers that depend on such constraints must
// check them after decoding.
func ValidateToolInput(schema map[string]any, input json.RawMessage) error {
	var payload any
	if err := json.Unmarshal(input, &payload); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return validateValue(schema, payload, "")
}

// validateValue recursively validates a value against a JSON Schema node.
// path is the dot-separated field path for error messages (empty at root).
func validateValue(schema map[string]any, value any, path string) error {
	if schema == nil {
		return nil
	}

	// Type check. "type" may be a single string ("string") or a union of
	// types expressed as an array ("["string","null"]"), per JSON Schema.
	if types, ok := schemaTypes(schema); ok {
		if err := checkType(types, value, path); err != nil {
			return err
		}
	}

	// Enum check.
	if enumVals, ok := schema["enum"].([]any); ok && value != nil {
		valid := false
		for _, ev := range enumVals {
			if fmt.Sprintf("%v", ev) == fmt.Sprintf("%v", value) {
				valid = true
				break
			}
		}
		if !valid {
			label := path
			if label == "" {
				label = "value"
			}
			return fmt.Errorf("%s: value %v not in enum %v", label, value, enumVals)
		}
	}

	obj, isObj := value.(map[string]any)

	// Required fields (only meaningful for objects). Schemas created in Go may
	// use []string, while JSON-decoded schemas use []any.
	if isObj {
		var required []string
		switch fields := schema["required"].(type) {
		case []any:
			for _, field := range fields {
				name, _ := field.(string)
				required = append(required, name)
			}
		case []string:
			required = fields
		}
		for _, field := range required {
			if _, present := obj[field]; !present {
				fieldPath := field
				if path != "" {
					fieldPath = path + "." + field
				}
				return fmt.Errorf("missing required field %q", fieldPath)
			}
		}
	}

	// Validate object properties recursively.
	if isObj {
		if props, ok := schema["properties"].(map[string]any); ok {
			for fieldName, propRaw := range props {
				propSchema, _ := propRaw.(map[string]any)
				if propSchema == nil {
					continue
				}
				fieldVal, present := obj[fieldName]
				if !present {
					continue // not present and not required — skip
				}
				fieldPath := fieldName
				if path != "" {
					fieldPath = path + "." + fieldName
				}
				if err := validateValue(propSchema, fieldVal, fieldPath); err != nil {
					return err
				}
			}
		}
	}

	// Validate array items recursively.
	if arr, isArr := value.([]any); isArr {
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range arr {
				itemPath := fmt.Sprintf("%s[%d]", path, i)
				if err := validateValue(itemSchema, item, itemPath); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// schemaTypes extracts the "type" constraint from a schema node, normalizing
// both the plain-string form ("type": "string") and the union/array form
// ("type": ["string", "null"]) into a slice of type names. ok is false when
// "type" is absent or not a recognizable shape, meaning no type constraint
// applies.
func schemaTypes(schema map[string]any) ([]string, bool) {
	switch t := schema["type"].(type) {
	case string:
		return []string{t}, true
	case []any:
		types := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				types = append(types, s)
			}
		}
		if len(types) == 0 {
			return nil, false
		}
		return types, true
	case []string:
		if len(t) == 0 {
			return nil, false
		}
		return t, true
	default:
		return nil, false
	}
}

// checkType verifies that value matches one of the expected JSON Schema
// types. null only satisfies the constraint when "null" is explicitly one of
// the allowed types (either as the sole type or as part of a union such as
// ["string", "null"]); otherwise a null value is rejected.
func checkType(types []string, value any, path string) error {
	label := path
	if label == "" {
		label = "value"
	}

	if value == nil {
		for _, t := range types {
			if t == "null" {
				return nil
			}
		}
		return fmt.Errorf("%s: expected %s, got null", label, strings.Join(types, " or "))
	}

	var lastErr error
	for _, t := range types {
		if err := checkSingleType(t, value, label); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return nil
}

// checkSingleType verifies that value matches a single JSON Schema type
// name. Unrecognized type names impose no constraint.
func checkSingleType(schemaType string, value any, label string) error {
	switch schemaType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: expected string, got %T", label, value)
		}
	case "integer":
		// JSON numbers unmarshal as float64; check it has no fractional part.
		f, ok := value.(float64)
		if !ok {
			return fmt.Errorf("%s: expected integer, got %T", label, value)
		}
		if f != float64(int64(f)) {
			return fmt.Errorf("%s: expected integer, got fractional number %v", label, f)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s: expected number, got %T", label, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean, got %T", label, value)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("%s: expected array, got %T", label, value)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("%s: expected object, got %T", label, value)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s: expected null, got %T", label, value)
		}
	}
	return nil
}
