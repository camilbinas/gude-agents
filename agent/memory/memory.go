// Package memory provides types shared across memory backends.
package memory

// Entry wraps a user-defined value with its similarity score and storage ID.
type Entry[T any] struct {
	ID    string // Storage-level ID (use with Forget to delete this entry)
	Value T
	Score float64
}
