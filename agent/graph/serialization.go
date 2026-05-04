package graph

import (
	"encoding/json"
	"fmt"
)

// StateSerializationError is returned when state contains non-serializable values.
type StateSerializationError struct {
	Key  string
	Type string
	Err  error
}

func (e *StateSerializationError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("graph: state key %q (type %s) is not JSON-serializable: %v", e.Key, e.Type, e.Err)
	}
	return fmt.Sprintf("graph: state is not JSON-serializable: %v", e.Err)
}

func (e *StateSerializationError) Unwrap() error {
	return e.Err
}

// validateStateSerializable checks that all values in the state map
// can be serialized to JSON. Returns an error identifying the first
// non-serializable key.
func validateStateSerializable(state State) error {
	_, err := json.Marshal(map[string]any(state))
	if err != nil {
		for k, v := range state {
			if _, e := json.Marshal(v); e != nil {
				return &StateSerializationError{
					Key:  k,
					Type: fmt.Sprintf("%T", v),
					Err:  e,
				}
			}
		}
		return &StateSerializationError{Err: err}
	}
	return nil
}
