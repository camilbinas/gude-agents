package postgres

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/jackc/pgx/v5/pgxpool"
)

type schemaCacheTestEntry struct {
	ID      string `db:"id,pk"`
	Tenant  string `db:"tenant,identifier"`
	Content string `db:"content,content"`
}

type schemaTestEmbedder struct{}

func (schemaTestEmbedder) Embed(context.Context, string) ([]float64, error) {
	return []float64{1}, nil
}

type countingSchemaTestEmbedder struct {
	calls int
}

func (e *countingSchemaTestEmbedder) Embed(context.Context, string) ([]float64, error) {
	e.calls++
	return []float64{1}, nil
}

var (
	_ memory.Memory[schemaCacheTestEntry] = (*Store[schemaCacheTestEntry])(nil)
	_ rag.Embedder                        = schemaTestEmbedder{}
)

func newSchemaTestStore(t *testing.T) *Store[schemaCacheTestEntry] {
	t.Helper()
	store, err := NewStore[schemaCacheTestEntry](&pgxpool.Pool{}, schemaTestEmbedder{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestParseSchemaReturnsIndependentConfigurationCopies(t *testing.T) {
	first, err := parseSchema[schemaCacheTestEntry]("embedding_one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseSchema[schemaCacheTestEntry]("embedding_two")
	if err != nil {
		t.Fatal(err)
	}

	if first == second || &first.Columns[0] == &second.Columns[0] {
		t.Fatal("schema callers must not share mutable cached state")
	}
	if first.EmbeddingCol != "embedding_one" || second.EmbeddingCol != "embedding_two" {
		t.Fatalf("embedding configuration leaked between schemas: first=%q second=%q", first.EmbeddingCol, second.EmbeddingCol)
	}

	first.Columns[0].Column = "mutated"
	third, err := parseSchema[schemaCacheTestEntry]("embedding_three")
	if err != nil {
		t.Fatal(err)
	}
	if third.Columns[0].Column == "mutated" {
		t.Fatal("schema cache was mutated through a returned schema")
	}
}

func TestNewStoreKeepsEmbeddingConfigurationPerStore(t *testing.T) {
	pool := &pgxpool.Pool{}
	first, err := NewStore[schemaCacheTestEntry](pool, schemaTestEmbedder{}, 1, WithEmbeddingColumn("embedding_one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore[schemaCacheTestEntry](pool, schemaTestEmbedder{}, 1, WithEmbeddingColumn("embedding_two"))
	if err != nil {
		t.Fatal(err)
	}

	if first.schema == second.schema || &first.schema.Columns[0] == &second.schema.Columns[0] {
		t.Fatal("stores must not share mutable schemas")
	}
	if first.schema.EmbeddingCol != first.embeddingCol {
		t.Fatalf("first store schema embedding = %q, want %q", first.schema.EmbeddingCol, first.embeddingCol)
	}
	if second.schema.EmbeddingCol != second.embeddingCol {
		t.Fatalf("second store schema embedding = %q, want %q", second.schema.EmbeddingCol, second.embeddingCol)
	}
	if first.embeddingCol == second.embeddingCol {
		t.Fatal("test setup expected different embedding columns")
	}
}

func TestNewStoreRejectsUnsupportedDistanceMetric(t *testing.T) {
	_, err := NewStore[schemaCacheTestEntry](
		&pgxpool.Pool{},
		schemaTestEmbedder{},
		1,
		WithDistanceMetric("cosine; DROP TABLE memories"),
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported distance metric") {
		t.Fatalf("err = %v, want unsupported distance metric", err)
	}
}

func TestParseSchemaRejectsPointerGenericType(t *testing.T) {
	if _, err := parseSchema[schemaCacheTestEntry]("embedding"); err != nil {
		t.Fatalf("value type must remain supported: %v", err)
	}

	_, err := parseSchema[*schemaCacheTestEntry]("embedding")
	if err == nil {
		t.Fatal("expected pointer generic type to be rejected")
	}
	if !strings.Contains(err.Error(), "T must be a non-pointer struct") || !strings.Contains(err.Error(), "*postgres.schemaCacheTestEntry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecallRejectsInvalidQueryBeforeEmbedding(t *testing.T) {
	tests := []struct {
		name  string
		query memory.RecallQuery
	}{
		{name: "empty text", query: memory.RecallQuery{}},
		{name: "negative limit", query: memory.RecallQuery{Text: "query", Limit: -1}},
		{name: "negative similarity", query: memory.RecallQuery{Text: "query", MinSimilarity: -0.1}},
		{name: "similarity above one", query: memory.RecallQuery{Text: "query", MinSimilarity: 1.1}},
		{name: "nan similarity", query: memory.RecallQuery{Text: "query", MinSimilarity: math.NaN()}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			embedder := &countingSchemaTestEmbedder{}
			store, err := NewStore[schemaCacheTestEntry](&pgxpool.Pool{}, embedder, 1)
			if err != nil {
				t.Fatal(err)
			}

			_, err = store.Recall(context.Background(), "tenant-1", test.query)
			if !errors.Is(err, memory.ErrInvalidRecallQuery) {
				t.Fatalf("err = %v, want memory.ErrInvalidRecallQuery", err)
			}
			if embedder.calls != 0 {
				t.Fatalf("embed calls = %d, want 0", embedder.calls)
			}
		})
	}
}

func TestRecallRejectsUnsupportedFilterBeforeEmbedding(t *testing.T) {
	embedder := &countingSchemaTestEmbedder{}
	store, err := NewStore[schemaCacheTestEntry](&pgxpool.Pool{}, embedder, 1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Recall(context.Background(), "tenant-1", memory.RecallQuery{
		Text: "query",
		Filters: []memory.Filter{{
			Field:    "content; DROP TABLE memories",
			Operator: memory.FilterEqual,
			Value:    "value",
		}},
	})
	if !errors.Is(err, memory.ErrUnsupportedFilter) {
		t.Fatalf("err = %v, want memory.ErrUnsupportedFilter", err)
	}
	if embedder.calls != 0 {
		t.Fatalf("embed calls = %d, want 0", embedder.calls)
	}
}

func TestBuildRecallQueryDefaultOrderAndUnlimitedResults(t *testing.T) {
	store := newSchemaTestStore(t)
	sql, args, err := store.buildRecallQuery(memory.RecallQuery{Text: "query"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `WHERE "tenant" = $2`) {
		t.Fatalf("query does not scope by identity: %s", sql)
	}
	if !strings.HasSuffix(sql, `ORDER BY "embedding" <=> $1`) {
		t.Fatalf("query does not use default similarity order: %s", sql)
	}
	if strings.Contains(sql, " LIMIT ") {
		t.Fatalf("zero limit must omit LIMIT: %s", sql)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none", args)
	}
}

func TestBuildRecallQueryPlacesSimilarityFiltersAndLimitArguments(t *testing.T) {
	store := newSchemaTestStore(t)
	hostileValue := `value'); DROP TABLE memories; --`
	ids := []string{"one", "two"}
	sql, args, err := store.buildRecallQuery(memory.RecallQuery{
		Text:          "query",
		Limit:         4,
		MinSimilarity: 0.75,
		Filters: []memory.Filter{
			{Field: "content", Operator: memory.FilterEqual, Value: hostileValue},
			{Field: "id", Operator: memory.FilterIn, Value: ids},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, fragment := range []string{
		`1 - ("embedding" <=> $1) >= $3`,
		`"content" = $4`,
		`"id" = ANY($5)`,
		`LIMIT $6`,
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("query %q does not contain %q", sql, fragment)
		}
	}
	if strings.Contains(sql, hostileValue) {
		t.Fatalf("filter value was interpolated into SQL: %s", sql)
	}
	wantArgs := []any{0.75, hostileValue, ids, 4}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestBuildRecallQueryMapsPortableFilterOperators(t *testing.T) {
	store := newSchemaTestStore(t)
	tests := []struct {
		name     string
		operator memory.FilterOperator
		value    any
		fragment string
	}{
		{name: "equal", operator: memory.FilterEqual, value: "x", fragment: `"content" = $3`},
		{name: "not equal", operator: memory.FilterNotEqual, value: "x", fragment: `"content" <> $3`},
		{name: "greater", operator: memory.FilterGreaterThan, value: time.Unix(1, 0), fragment: `"content" > $3`},
		{name: "greater equal", operator: memory.FilterGreaterThanOrEqual, value: 1, fragment: `"content" >= $3`},
		{name: "less", operator: memory.FilterLessThan, value: 1, fragment: `"content" < $3`},
		{name: "less equal", operator: memory.FilterLessThanOrEqual, value: 1, fragment: `"content" <= $3`},
		{name: "in", operator: memory.FilterIn, value: []string{"x"}, fragment: `"content" = ANY($3)`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sql, args, err := store.buildRecallQuery(memory.RecallQuery{
				Text: "query",
				Filters: []memory.Filter{{
					Field:    "content",
					Operator: test.operator,
					Value:    test.value,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(sql, test.fragment) {
				t.Fatalf("query %q does not contain %q", sql, test.fragment)
			}
			if len(args) != 1 || !reflect.DeepEqual(args[0], test.value) {
				t.Fatalf("args = %#v, want [%#v]", args, test.value)
			}
		})
	}
}

func TestBuildRecallQueryPreservesFilterAndOrderPrecedence(t *testing.T) {
	store := newSchemaTestStore(t)
	sql, args, err := store.buildRecallQuery(memory.RecallQuery{
		Text: "query",
		Filters: []memory.Filter{
			{Field: "content", Operator: memory.FilterEqual, Value: "first"},
			{Field: "tenant", Operator: memory.FilterNotEqual, Value: "second"},
		},
		Order: []memory.Order{
			{Field: "content", Direction: memory.OrderAscending},
			{Field: "id", Direction: memory.OrderDescending},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `"content" = $3 AND "tenant" <> $4`) {
		t.Fatalf("filter order or AND semantics not preserved: %s", sql)
	}
	if !strings.HasSuffix(sql, `ORDER BY "content" ASC, "id" DESC`) {
		t.Fatalf("order precedence not preserved: %s", sql)
	}
	if !reflect.DeepEqual(args, []any{"first", "second"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildRecallQueryRejectsUnsupportedFilters(t *testing.T) {
	store := newSchemaTestStore(t)
	var nilStrings []string
	tests := []struct {
		name   string
		filter memory.Filter
	}{
		{name: "empty field", filter: memory.Filter{Operator: memory.FilterEqual, Value: "x"}},
		{name: "unknown field", filter: memory.Filter{Field: "missing", Operator: memory.FilterEqual, Value: "x"}},
		{name: "injected field", filter: memory.Filter{Field: `content" DESC; --`, Operator: memory.FilterEqual, Value: "x"}},
		{name: "unknown operator", filter: memory.Filter{Field: "content", Operator: memory.FilterOperator("contains"), Value: "x"}},
		{name: "in scalar", filter: memory.Filter{Field: "content", Operator: memory.FilterIn, Value: "x"}},
		{name: "in nil slice", filter: memory.Filter{Field: "content", Operator: memory.FilterIn, Value: nilStrings}},
		{name: "in untyped elements", filter: memory.Filter{Field: "content", Operator: memory.FilterIn, Value: []any{"x"}}},
		{name: "comparison slice", filter: memory.Filter{Field: "content", Operator: memory.FilterEqual, Value: []string{"x"}}},
		{name: "comparison map", filter: memory.Filter{Field: "content", Operator: memory.FilterEqual, Value: map[string]string{"x": "y"}}},
		{name: "comparison struct", filter: memory.Filter{Field: "content", Operator: memory.FilterEqual, Value: struct{}{}}},
		{name: "nil value", filter: memory.Filter{Field: "content", Operator: memory.FilterEqual, Value: nil}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := store.buildRecallQuery(memory.RecallQuery{Text: "query", Filters: []memory.Filter{test.filter}})
			if !errors.Is(err, memory.ErrUnsupportedFilter) {
				t.Fatalf("err = %v, want memory.ErrUnsupportedFilter", err)
			}
		})
	}
}

func TestBuildRecallQueryRejectsUnsupportedOrder(t *testing.T) {
	store := newSchemaTestStore(t)
	tests := []memory.Order{
		{Field: "missing", Direction: memory.OrderAscending},
		{Field: `content"; DROP TABLE memories`, Direction: memory.OrderAscending},
		{Field: "content", Direction: memory.OrderDirection("sideways")},
	}
	for _, order := range tests {
		_, _, err := store.buildRecallQuery(memory.RecallQuery{Text: "query", Order: []memory.Order{order}})
		if !errors.Is(err, memory.ErrUnsupportedOrder) {
			t.Errorf("order %#v: err = %v, want memory.ErrUnsupportedOrder", order, err)
		}
	}
}

// Memory tools resolve the partition key strictly before touching the pool,
// so a zero Store is enough to exercise the error paths.
func TestToolsRequireIdentityOrScope(t *testing.T) {
	store := &Store[schemaCacheTestEntry]{}
	cases := map[string]func() (string, error){
		"remember": func() (string, error) {
			return NewRememberTool(store).Handler(agent.Background(), []byte(`{"content":"x"}`))
		},
		"remember scoped no fallback": func() (string, error) {
			return NewRememberTool(store, WithScope("tenant")).Handler(agent.Background().WithIdentity("user-1"), []byte(`{"content":"x"}`))
		},
		"recall": func() (string, error) {
			return NewRecallTool(store).Handler(agent.Background(), []byte(`{"query":"x"}`))
		},
		"update": func() (string, error) {
			return NewUpdateTool(store).Handler(agent.Background(), []byte(`{"id":"x","content":"y"}`))
		},
		"forget scoped": func() (string, error) {
			return NewForgetTool(store, WithScope("tenant")).Handler(agent.Background().WithIdentity("user-1"), []byte(`{"id":"x"}`))
		},
	}
	for name, call := range cases {
		if _, err := call(); !errors.Is(err, memory.ErrMissingIdentity) {
			t.Errorf("%s: err = %v, want memory.ErrMissingIdentity", name, err)
		}
	}
}
