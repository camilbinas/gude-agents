package postgres

import (
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestScoreExpr_CosineIsOneMinusDistance verifies the cosine metric uses the
// "1 - distance" formula, which is valid because cosine distance is bounded
// to [0, 2].
func TestScoreExpr_CosineIsOneMinusDistance(t *testing.T) {
	got := scoreExpr(`"embedding"`, "<=>", "$1")
	want := `1 - ("embedding" <=> $1)`
	if got != want {
		t.Errorf("scoreExpr(cosine) = %q, want %q", got, want)
	}
}

// TestScoreExpr_InnerProductNegatesPgvectorOperator verifies the
// inner_product metric negates pgvector's <#> result (which is the negative
// inner product) back to the plain inner product, rather than reusing the
// cosine "1 - x" formula.
func TestScoreExpr_InnerProductNegatesPgvectorOperator(t *testing.T) {
	got := scoreExpr(`"embedding"`, "<#>", "$1")
	want := `-("embedding" <#> $1)`
	if got != want {
		t.Errorf("scoreExpr(inner_product) = %q, want %q", got, want)
	}
	if strings.Contains(got, "1 -") {
		t.Errorf("scoreExpr(inner_product) = %q must not reuse the cosine '1 - x' formula", got)
	}
}

// TestScoreExpr_L2IsBoundedReciprocal verifies the l2 metric does not use
// "1 - distance" (which is unbounded below for Euclidean distance) and
// instead uses a formula bounded to (0, 1].
func TestScoreExpr_L2IsBoundedReciprocal(t *testing.T) {
	got := scoreExpr(`"embedding"`, "<->", "$1")
	want := `1 / (1 + ("embedding" <-> $1))`
	if got != want {
		t.Errorf("scoreExpr(l2) = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, "1 - ") {
		t.Errorf("scoreExpr(l2) = %q must not use the unbounded '1 - distance' formula", got)
	}
}

// TestBuildRecallQuery_L2UsesBoundedScoreExpr verifies that a Store
// configured for the l2 metric builds a Recall query using the bounded
// reciprocal score expression rather than "1 - distance".
func TestBuildRecallQuery_L2UsesBoundedScoreExpr(t *testing.T) {
	store, err := NewStore[schemaCacheTestEntry](&pgxpool.Pool{}, schemaTestEmbedder{}, 1, WithDistanceMetric("l2"))
	if err != nil {
		t.Fatal(err)
	}

	sql, _, err := store.buildRecallQuery(memoryRecallQueryWithMinSimilarity())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `1 / (1 + ("embedding" <-> $1))`) {
		t.Errorf("l2 query does not use the bounded score expression: %s", sql)
	}
	if strings.Contains(sql, `1 - ("embedding" <->`) {
		t.Errorf("l2 query incorrectly uses the cosine '1 - distance' formula: %s", sql)
	}
}

// TestBuildRecallQuery_InnerProductUsesNegatedScoreExpr verifies that a
// Store configured for the inner_product metric builds a Recall query using
// the negated-operator score expression rather than "1 - distance".
func TestBuildRecallQuery_InnerProductUsesNegatedScoreExpr(t *testing.T) {
	store, err := NewStore[schemaCacheTestEntry](&pgxpool.Pool{}, schemaTestEmbedder{}, 1, WithDistanceMetric("inner_product"))
	if err != nil {
		t.Fatal(err)
	}

	sql, _, err := store.buildRecallQuery(memoryRecallQueryWithMinSimilarity())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `-("embedding" <#> $1)`) {
		t.Errorf("inner_product query does not use the negated score expression: %s", sql)
	}
	if strings.Contains(sql, `1 - ("embedding" <#>`) {
		t.Errorf("inner_product query incorrectly uses the cosine '1 - distance' formula: %s", sql)
	}
}

func memoryRecallQueryWithMinSimilarity() memory.RecallQuery {
	return memory.RecallQuery{Text: "query", MinSimilarity: 0.5}
}
