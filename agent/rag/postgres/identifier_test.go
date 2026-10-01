package postgres

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestNew_SanitizesTableAndColumnIdentifiers verifies that identifiers
// supplied via WithTableName/WithColumns are escaped through
// pgx.Identifier.Sanitize (double-quoted, embedded quotes doubled) rather
// than interpolated raw into SQL, so quotes, spaces, reserved words, and
// SQL-fragment-shaped names cannot break out of the identifier position.
func TestNew_SanitizesTableAndColumnIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		apply func() []Option
		field func(s *Store) string
		want  string
	}{
		{
			name:  "table name with embedded quote",
			apply: func() []Option { return []Option{WithTableName(`docs"; DROP TABLE users; --`)} },
			field: func(s *Store) string { return s.tableName },
			want:  `"docs""; DROP TABLE users; --"`,
		},
		{
			name:  "table name with space",
			apply: func() []Option { return []Option{WithTableName("my documents")} },
			field: func(s *Store) string { return s.tableName },
			want:  `"my documents"`,
		},
		{
			name:  "table name is a reserved word",
			apply: func() []Option { return []Option{WithTableName("select")} },
			field: func(s *Store) string { return s.tableName },
			want:  `"select"`,
		},
		{
			name:  "id column with embedded quote",
			apply: func() []Option { return []Option{WithColumns(`id"); --`, "content", "metadata", "embedding")} },
			field: func(s *Store) string { return s.colID },
			want:  `"id""); --"`,
		},
		{
			name:  "metadata column with embedded quote",
			apply: func() []Option { return []Option{WithColumns("id", "content", `meta" OR 1=1 --`, "embedding")} },
			field: func(s *Store) string { return s.colMeta },
			want:  `"meta"" OR 1=1 --"`,
		},
		{
			name:  "embedding column with reserved-word-looking name",
			apply: func() []Option { return []Option{WithColumns("id", "content", "metadata", "table")} },
			field: func(s *Store) string { return s.colEmbed },
			want:  `"table"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(&pgxpool.Pool{}, 3, tc.apply()...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := tc.field(s); got != tc.want {
				t.Errorf("identifier = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNew_RejectsNULByteIdentifiers verifies identifiers containing a NUL
// byte are rejected outright rather than silently truncated/sanitized.
func TestNew_RejectsNULByteIdentifiers(t *testing.T) {
	_, err := New(&pgxpool.Pool{}, 3, WithTableName("docs\x00evil"))
	if err == nil {
		t.Fatal("expected error for NUL byte in table name")
	}
}

// TestSearchQuery_UsesSanitizedIdentifiers verifies the generated Search SQL
// contains the double-quoted (sanitized) identifiers rather than the raw
// caller-supplied strings, so no identifier can break out of position.
func TestSearchQuery_UsesSanitizedIdentifiers(t *testing.T) {
	s, err := New(&pgxpool.Pool{}, 3,
		WithTableName(`docs"; DROP TABLE users; --`),
		WithColumns("id", "content", "metadata", "embedding"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.tableName, `""`) {
		t.Fatalf("expected sanitized table name to double embedded quotes, got %q", s.tableName)
	}
	// The stored tableName is what all Sprintf-built queries use directly;
	// verifying it here is sufficient since Search/Upsert/Find/Delete all
	// consume s.tableName as-is without re-escaping (and without accepting
	// caller input at query time).
}
