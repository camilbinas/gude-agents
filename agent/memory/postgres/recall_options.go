package postgres

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/memory"
)

func validateRecallQuery(query memory.RecallQuery) error {
	if query.Text == "" {
		return fmt.Errorf("%w: text must not be empty", memory.ErrInvalidRecallQuery)
	}
	if query.Limit < 0 {
		return fmt.Errorf("%w: limit must not be negative", memory.ErrInvalidRecallQuery)
	}
	if query.MinSimilarity < 0 || query.MinSimilarity > 1 || query.MinSimilarity != query.MinSimilarity {
		return fmt.Errorf("%w: minimum similarity must be between 0 and 1", memory.ErrInvalidRecallQuery)
	}
	return nil
}

// buildRecallQuery maps a portable query to parameterized PostgreSQL SQL. The
// returned arguments start at $3 because $1 and $2 are reserved for the query
// vector and identity respectively.
func (s *Store[T]) buildRecallQuery(query memory.RecallQuery) (string, []any, error) {
	if err := validateRecallQuery(query); err != nil {
		return "", nil, err
	}

	op := s.distanceOp()
	score := scoreExpr(s.embeddingCol, op, "$1")
	identifierCol := s.schema.Columns[s.schema.IdentifierIdx].Column
	selectCols := strings.Join(s.schema.columnNames(), ", ")

	var sql strings.Builder
	fmt.Fprintf(
		&sql,
		"SELECT %s, %s AS _similarity FROM %s WHERE %s = $2",
		selectCols, score, s.tableName, identifierCol,
	)

	args := make([]any, 0, len(query.Filters)+2)
	paramIdx := 3
	if query.MinSimilarity != 0 {
		fmt.Fprintf(&sql, " AND %s >= $%d", score, paramIdx)
		args = append(args, query.MinSimilarity)
		paramIdx++
	}

	for i, filter := range query.Filters {
		column, ok := s.schema.recallColumn(filter.Field)
		if !ok {
			return "", nil, fmt.Errorf("%w: filter %d field %q", memory.ErrUnsupportedFilter, i, filter.Field)
		}

		operator, err := postgresFilterOperator(filter.Operator)
		if err != nil {
			return "", nil, fmt.Errorf("filter %d: %w", i, err)
		}
		if err := validateFilterValue(filter.Operator, filter.Value); err != nil {
			return "", nil, fmt.Errorf("filter %d field %q: %w", i, filter.Field, err)
		}

		if filter.Operator == memory.FilterIn {
			fmt.Fprintf(&sql, " AND %s = ANY($%d)", column, paramIdx)
		} else {
			fmt.Fprintf(&sql, " AND %s %s $%d", column, operator, paramIdx)
		}
		args = append(args, filter.Value)
		paramIdx++
	}

	if len(query.Order) == 0 {
		fmt.Fprintf(&sql, " ORDER BY %s %s $1", s.embeddingCol, op)
	} else {
		orders := make([]string, len(query.Order))
		for i, order := range query.Order {
			column, ok := s.schema.recallColumn(order.Field)
			if !ok {
				return "", nil, fmt.Errorf("%w: order %d field %q", memory.ErrUnsupportedOrder, i, order.Field)
			}
			direction, err := postgresOrderDirection(order.Direction)
			if err != nil {
				return "", nil, fmt.Errorf("order %d: %w", i, err)
			}
			orders[i] = column + " " + direction
		}
		fmt.Fprintf(&sql, " ORDER BY %s", strings.Join(orders, ", "))
	}

	if query.Limit > 0 {
		fmt.Fprintf(&sql, " LIMIT $%d", paramIdx)
		args = append(args, query.Limit)
	}

	return sql.String(), args, nil
}

func postgresFilterOperator(operator memory.FilterOperator) (string, error) {
	switch operator {
	case memory.FilterEqual:
		return "=", nil
	case memory.FilterNotEqual:
		return "<>", nil
	case memory.FilterGreaterThan:
		return ">", nil
	case memory.FilterGreaterThanOrEqual:
		return ">=", nil
	case memory.FilterLessThan:
		return "<", nil
	case memory.FilterLessThanOrEqual:
		return "<=", nil
	case memory.FilterIn:
		return "= ANY", nil
	default:
		return "", fmt.Errorf("%w: operator %q", memory.ErrUnsupportedFilter, operator)
	}
}

func postgresOrderDirection(direction memory.OrderDirection) (string, error) {
	switch direction {
	case memory.OrderAscending:
		return "ASC", nil
	case memory.OrderDescending:
		return "DESC", nil
	default:
		return "", fmt.Errorf("%w: direction %q", memory.ErrUnsupportedOrder, direction)
	}
}

func validateFilterValue(operator memory.FilterOperator, value any) error {
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return fmt.Errorf("%w: nil value", memory.ErrUnsupportedFilter)
	}

	if operator == memory.FilterIn {
		if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
			return fmt.Errorf("%w: operator %q requires a typed slice or array, got %T", memory.ErrUnsupportedFilter, operator, value)
		}
		if v.Kind() == reflect.Slice && v.IsNil() {
			return fmt.Errorf("%w: operator %q does not support a nil slice", memory.ErrUnsupportedFilter, operator)
		}
		if !supportedArrayElement(v.Type().Elem()) {
			return fmt.Errorf("%w: operator %q does not support %T", memory.ErrUnsupportedFilter, operator, value)
		}
		return nil
	}

	if isNilValue(v) || !supportedScalarType(v.Type()) {
		return fmt.Errorf("%w: operator %q does not support %T", memory.ErrUnsupportedFilter, operator, value)
	}
	return nil
}

func supportedArrayElement(t reflect.Type) bool {
	if t.Kind() == reflect.Interface || t.Kind() == reflect.Slice || t.Kind() == reflect.Map || t.Kind() == reflect.Func || t.Kind() == reflect.Chan || t.Kind() == reflect.UnsafePointer {
		return false
	}
	if t.Kind() == reflect.Ptr {
		return supportedScalarType(t.Elem())
	}
	return supportedScalarType(t)
}

func supportedScalarType(t reflect.Type) bool {
	if t == reflect.TypeFor[time.Time]() {
		return true
	}
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	case reflect.Ptr:
		return supportedScalarType(t.Elem())
	default:
		return false
	}
}

func isNilValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
