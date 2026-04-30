// Package memory provides types shared across memory backends.
package memory

import "context"

// Memory is the core interface that all memory backends implement.
// It covers the minimum viable operations: storing and retrieving entries.
type Memory[T any] interface {
	Remember(ctx context.Context, identifier string, value T) error
	Recall(ctx context.Context, identifier string, query string, limit int, opts ...RecallOption) ([]Entry[T], error)
}

// Updater is implemented by backends that support in-place updates.
type Updater[T any] interface {
	Update(ctx context.Context, identifier, id string, value T) error
}

// Forgetter is implemented by backends that support removing a single entry.
type Forgetter[T any] interface {
	Forget(ctx context.Context, identifier, id string) error
}

// BulkForgetter is implemented by backends that support removing all entries
// for a given identifier (e.g. GDPR deletion).
type BulkForgetter[T any] interface {
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
