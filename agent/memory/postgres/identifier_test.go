package postgres

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestNewStore_SanitizesIdentifiers verifies that table/column identifiers
// supplied via WithTableName/WithEmbeddingColumn and struct `db` tags are
// escaped through pgx.Identifier.Sanitize (double-quoted, embedded quotes
// doubled) rather than interpolated raw into SQL, so quotes, spaces,
// reserved words, and SQL-fragment-shaped names cannot break out of the
// identifier position. NewStore does not connect to the database, so this
// runs without a live Postgres instance.
func TestNewStore_SanitizesIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		table string
		embed string
	}{
		{
			name:  "table name with embedded quote and SQL fragment",
			table: `docs"; DROP TABLE users; --`,
			embed: "embedding",
		},
		{
			name:  "table name with space",
			table: "my memories",
			embed: "embedding",
		},
		{
			name:  "table name is a reserved word",
			table: "select",
			embed: "embedding",
		},
		{
			name:  "embedding column with embedded quote",
			table: "memories",
			embed: `embedding"); --`,
		},
		{
			name:  "embedding column is a reserved word",
			table: "memories",
			embed: "table",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewStore[schemaCacheTestEntry](&pgxpool.Pool{}, schemaTestEmbedder{}, 1,
				WithTableName(tc.table), WithEmbeddingColumn(tc.embed))
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}

			wantTable := `"` + strings.ReplaceAll(tc.table, `"`, `""`) + `"`
			if store.tableName != wantTable {
				t.Errorf("tableName = %q, want %q", store.tableName, wantTable)
			}
			wantEmbed := `"` + strings.ReplaceAll(tc.embed, `"`, `""`) + `"`
			if store.embeddingCol != wantEmbed {
				t.Errorf("embeddingCol = %q, want %q", store.embeddingCol, wantEmbed)
			}
		})
	}
}

// TestNewStore_SanitizesStructColumnIdentifiers verifies that column names
// derived from `db` struct tags are also sanitized, using the recall query
// builder's SELECT column list as evidence they're double-quoted rather than
// raw.
func TestNewStore_SanitizesStructColumnIdentifiers(t *testing.T) {
	store := newSchemaTestStore(t)
	sql, _, err := store.buildRecallQuery(memoryRecallQueryWithMinSimilarity())
	if err != nil {
		t.Fatal(err)
	}
	// schemaCacheTestEntry's columns (id, tenant, content) must appear
	// double-quoted in the generated SELECT, confirming sanitization ran.
	for _, col := range []string{`"id"`, `"tenant"`, `"content"`} {
		if !strings.Contains(sql, col) {
			t.Errorf("query %q does not contain sanitized column %q", sql, col)
		}
	}
}

// TestNewStore_RejectsNULByteIdentifiers verifies identifiers containing a
// NUL byte are rejected outright rather than silently sanitized.
func TestNewStore_RejectsNULByteIdentifiers(t *testing.T) {
	_, err := NewStore[schemaCacheTestEntry](&pgxpool.Pool{}, schemaTestEmbedder{}, 1,
		WithTableName("memories\x00evil"))
	if err == nil {
		t.Fatal("expected error for NUL byte in table name")
	}
}
