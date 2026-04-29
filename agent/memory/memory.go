// Package memory provides types shared across memory backends.
package memory

import "context"

// Memory is the interface that all memory backends implement.
type Memory[T any] interface {
	Remember(ctx context.Context, identifier string, value T) error
	Recall(ctx context.Context, identifier string, query string, limit int, opts ...RecallOption) ([]Entry[T], error)
	Forget(ctx context.Context, identifier, id string) error
	ForgetAll(ctx context.Context, identifier string) error
}

// RecallOption configures filtering and sorting for Recall queries.
type RecallOption interface {
	IsRecallOption()
}

// Entry wraps a value with its similarity score and storage ID.
type Entry[T any] struct {
	ID    string
	Value T
	Score float64
}
