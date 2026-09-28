package postgres

import (
	"context"
	"strings"
	"testing"

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
