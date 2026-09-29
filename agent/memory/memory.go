// Package memory provides portable types shared across memory backends.
package memory

import (
	"context"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// Memory is the core interface implemented by memory backends. identity
// partitions entries, typically by invocation identity or a strict scope.
type Memory[T any] interface {
	Remember(ctx context.Context, identity string, value T) error
	Recall(ctx context.Context, identity string, query RecallQuery) ([]Entry[T], error)
}

// Updater is implemented by backends that support in-place updates.
type Updater[T any] interface {
	Update(ctx context.Context, identity, id string, value T) error
}

// Forgetter is implemented by backends that support removing one entry.
type Forgetter[T any] interface {
	Forget(ctx context.Context, identity, id string) error
}

// BulkForgetter is implemented by backends that support removing every entry
// associated with an identity.
type BulkForgetter[T any] interface {
	ForgetAll(ctx context.Context, identity string) error
}

// FilterOperator is a portable comparison operator for a memory field.
type FilterOperator string

const (
	FilterEqual              FilterOperator = "eq"
	FilterNotEqual           FilterOperator = "ne"
	FilterGreaterThan        FilterOperator = "gt"
	FilterGreaterThanOrEqual FilterOperator = "gte"
	FilterLessThan           FilterOperator = "lt"
	FilterLessThanOrEqual    FilterOperator = "lte"
	FilterIn                 FilterOperator = "in"
)

// Filter applies Operator to the backend field named Field and Value. Filters
// in one RecallQuery use AND semantics. Backends must return ErrUnsupportedFilter
// when they cannot implement a requested field, operator, or value type.
type Filter struct {
	Field    string
	Operator FilterOperator
	Value    any
}

// OrderDirection is the portable direction for an Order clause.
type OrderDirection string

const (
	OrderAscending  OrderDirection = "asc"
	OrderDescending OrderDirection = "desc"
)

// Order sorts results by the backend field named Field. Earlier entries in
// RecallQuery.Order have higher precedence. Backends must return
// ErrUnsupportedOrder when they cannot implement a requested field or direction.
type Order struct {
	Field     string
	Direction OrderDirection
}

// RecallQuery describes a portable memory lookup.
//
// Text is required and is used for semantic scoring. Limit zero means no result
// limit; a negative Limit is invalid. MinSimilarity zero disables thresholding;
// otherwise it must be in (0, 1]. Empty Filters and Order slices apply no
// filtering or explicit ordering, and results default to similarity descending.
type RecallQuery struct {
	Text          string
	Limit         int
	MinSimilarity float64
	Filters       []Filter
	Order         []Order
}

var (
	// ErrInvalidRecallQuery indicates invalid portable query values.
	ErrInvalidRecallQuery = errors.New("memory: invalid recall query")
	// ErrUnsupportedFilter indicates a backend cannot apply a requested filter.
	ErrUnsupportedFilter = errors.New("memory: unsupported filter")
	// ErrUnsupportedOrder indicates a backend cannot apply requested ordering.
	ErrUnsupportedOrder = errors.New("memory: unsupported order")
	// ErrMissingIdentity is returned (wrapped) when a memory tool cannot resolve
	// its invocation identity or configured strict scope.
	ErrMissingIdentity = errors.New("memory: identity not set on context")
)

// ResolveIdentity resolves the value that partitions memory entries. An empty
// scope uses agent.IdentityFrom; a non-empty scope strictly uses
// agent.ScopeFrom and never falls back to identity.
func ResolveIdentity(ctx context.Context, scope string) (string, error) {
	if scope != "" {
		value, ok := agent.ScopeFrom(ctx, scope)
		if !ok || value == "" {
			return "", fmt.Errorf("%w: scope %q not set; use c.WithScope(%q, value)", ErrMissingIdentity, scope, scope)
		}
		return value, nil
	}
	identity := agent.IdentityFrom(ctx)
	if identity == "" {
		return "", fmt.Errorf("%w; use c.WithIdentity(id)", ErrMissingIdentity)
	}
	return identity, nil
}

// Entry wraps a recalled value with its similarity score and storage ID.
type Entry[T any] struct {
	ID    string
	Value T
	Score float64
}
