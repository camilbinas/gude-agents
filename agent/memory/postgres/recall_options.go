package postgres

import (
	"fmt"
	"time"
)

// SortDir is the sort direction for ORDER BY clauses.
type SortDir string

const (
	Asc  SortDir = "ASC"
	Desc SortDir = "DESC"
)

// RecallOption configures filtering and sorting for typed Recall queries.
// It satisfies memory.RecallOption so it can be passed to the interface method,
// and also satisfies the Option interface for NewRecallTool.
type RecallOption func(*recallConfig)

func (RecallOption) IsRecallOption() {}

func (r RecallOption) applyTool(c *toolConfig) {
	c.recallOpts = append(c.recallOpts, r)
}

// recallConfig accumulates filters and ordering for a Recall query.
type recallConfig struct {
	filters       []filter
	orderBy       []orderClause
	minSimilarity *float64
}

type filter struct {
	sql  string
	args []any
}

type orderClause struct {
	column string
	dir    SortDir
}

// WithFieldEquals adds a WHERE column = value filter.
func WithFieldEquals(column string, value any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			sql:  fmt.Sprintf("%s = ?", column),
			args: []any{value},
		})
	}
}

// WithFieldGT adds a WHERE column > value filter.
func WithFieldGT(column string, value any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			sql:  fmt.Sprintf("%s > ?", column),
			args: []any{value},
		})
	}
}

// WithFieldLT adds a WHERE column < value filter.
func WithFieldLT(column string, value any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			sql:  fmt.Sprintf("%s < ?", column),
			args: []any{value},
		})
	}
}

// WithFieldIn adds a WHERE column = ANY($n) filter for slice values.
// The values slice is passed directly to pgx — use a typed slice (e.g.
// []string) for correct encoding.
func WithFieldIn(column string, values any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			sql:  fmt.Sprintf("%s = ANY(?)", column),
			args: []any{values},
		})
	}
}

// WithTimeAfter adds a WHERE column > timestamp filter.
func WithTimeAfter(column string, t time.Time) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			sql:  fmt.Sprintf("%s > ?", column),
			args: []any{t},
		})
	}
}

// WithTimeBefore adds a WHERE column < timestamp filter.
func WithTimeBefore(column string, t time.Time) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			sql:  fmt.Sprintf("%s < ?", column),
			args: []any{t},
		})
	}
}

// WithMinSimilarity sets a minimum similarity threshold. Only results with
// similarity >= this value are returned. This filters by vector distance
// before applying the limit.
func WithMinSimilarity(threshold float64) RecallOption {
	return func(c *recallConfig) {
		c.minSimilarity = &threshold
	}
}

// WithOrderBy adds an ORDER BY clause. Multiple calls append additional
// sort keys. Vector similarity is always the primary sort unless overridden
// by explicit ordering.
func WithOrderBy(column string, dir SortDir) RecallOption {
	return func(c *recallConfig) {
		c.orderBy = append(c.orderBy, orderClause{column: column, dir: dir})
	}
}

// WithRawFilter adds a raw SQL WHERE clause with parameterized arguments.
// Use ? as placeholder — they'll be renumbered to $N automatically.
func WithRawFilter(sql string, args ...any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{sql: sql, args: args})
	}
}

// buildWhereClause converts accumulated filters into a SQL WHERE fragment
// with properly numbered $N placeholders. startParam is the next available
// parameter number.
func (rc *recallConfig) buildWhereClause(startParam int) (string, []any, int) {
	if len(rc.filters) == 0 {
		return "", nil, startParam
	}

	var clauses []string
	var allArgs []any
	paramIdx := startParam

	for _, f := range rc.filters {
		clause := f.sql
		for _, arg := range f.args {
			placeholder := fmt.Sprintf("$%d", paramIdx)
			clause = replaceFirst(clause, "?", placeholder)
			allArgs = append(allArgs, arg)
			paramIdx++
		}
		clauses = append(clauses, clause)
	}

	return " AND " + joinAnd(clauses), allArgs, paramIdx
}

// buildOrderClause returns the ORDER BY fragment. If no explicit ordering
// is set, returns empty (caller uses vector similarity as default).
func (rc *recallConfig) buildOrderClause() string {
	if len(rc.orderBy) == 0 {
		return ""
	}
	parts := make([]string, len(rc.orderBy))
	for i, o := range rc.orderBy {
		parts[i] = fmt.Sprintf("%s %s", o.column, o.dir)
	}
	return joinComma(parts)
}

// replaceFirst replaces the first occurrence of old with new in s.
func replaceFirst(s, old, new string) string {
	i := indexOf(s, old)
	if i == -1 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func joinAnd(parts []string) string {
	result := parts[0]
	for _, p := range parts[1:] {
		result += " AND " + p
	}
	return result
}

func joinComma(parts []string) string {
	result := parts[0]
	for _, p := range parts[1:] {
		result += ", " + p
	}
	return result
}
