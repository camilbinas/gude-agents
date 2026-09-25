package postgres

import "encoding/json"

// jsonUnmarshal is a thin alias so tests read without an extra import.
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
