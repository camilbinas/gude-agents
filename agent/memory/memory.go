// Package memory provides types shared across memory backends.
package memory

import "context"

// Memory is the interface that all memory backends implement. It provides
// semantic storage and retrieval scoped by an identifier (e.g. user or session).
type Memory[T any] interface {
	// Remember stores a value for the given identifier. The value's content
	// field (tagged with db:"...,content") is embedded for later similarity search.
	Remember(ctx context.Context, identifier string, value T) error

	// Recall retrieves values by semantic similarity to the query, returning
	// at most limit results ordered by descending similarity score.
	// Backend-specific RecallOptions (filtering, sorting) are accepted and
	// silently ignored by backends that don't support them.
	Recall(ctx context.Context, identifier string, query string, limit int, opts ...RecallOption) ([]Entry[T], error)

	// Forget removes a single entry by its storage ID.
	Forget(ctx context.Context, identifier, id string) error

	// ForgetAll removes all entries for the given identifier.
	ForgetAll(ctx context.Context, identifier string) error
}

// RecallOption configures filtering and sorting for Recall queries.
// Backend packages (postgres, redis) provide concrete option constructors
// (e.g. WithFieldEquals, WithMinSimilarity) that satisfy this interface.
type RecallOption interface {
	IsRecallOption()
}

// Entry wraps a user-defined value with its similarity score and storage ID.
type Entry[T any] struct {
	ID    string // Storage-level ID (use with Forget to delete this entry)
	Value T
	Score float64
}
