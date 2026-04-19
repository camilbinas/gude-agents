package agent

import (
	"encoding/json"
	"fmt"
)

// ValidateToolInput checks that the JSON payload strictly satisfies the tool's
// declared schema. It validates recursively: required fields, type checking,
// enum constraints, nested objects, and array item types.
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

	// Type check.
	if schemaType, ok := schema["type"].(string); ok {
		if err := checkType(schemaType, value, path); err != nil {
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

	// Required fields (only meaningful for objects).
	if isObj {
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				field, _ := r.(string)
				if _, present := obj[field]; !present {
					fieldPath := field
					if path != "" {
						fieldPath = path + "." + field
					}
					return fmt.Errorf("missing required field %q", fieldPath)
				}
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

// checkType verifies that value matches the expected JSON Schema type.
func checkType(schemaType string, value any, path string) error {
	if value == nil {
		// null is only valid for nullable types; for now we allow it to avoid
		// breaking existing behaviour where optional fields may be null.
		return nil
	}
	label := path
	if label == "" {
		label = "value"
	}
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
	}
	return nil
}
