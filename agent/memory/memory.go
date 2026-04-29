// Package memory provides types shared across memory backends.
package memory

// Entry wraps a user-defined value with its similarity score from a recall query.
type Entry[T any] struct {
	Value T
	Score float64
}
