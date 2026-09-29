package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/rag"
	goredis "github.com/redis/go-redis/v9"
)

type redisTestEntry struct {
	ID      string `db:"id,pk"`
	Tenant  string `db:"tenant,identifier"`
	Content string `db:"content,content"`
}

type anotherRedisTestEntry struct {
	ID      string `db:"id,pk"`
	Tenant  string `db:"tenant,identifier"`
	Content string `db:"content,content"`
}

type redisTestEmbedder struct{}

func (redisTestEmbedder) Embed(context.Context, string) ([]float64, error) {
	return []float64{1}, nil
}

var _ rag.Embedder = redisTestEmbedder{}
var _ memory.Memory[redisTestEntry] = (*Store[redisTestEntry])(nil)

type fakeRedisClient struct {
	hashes   map[string]map[string]string
	hsets    []string
	dels     [][]string
	doResult any
	doCalls  [][]any
	doFunc   func([]any) (any, error)
}

func (c *fakeRedisClient) Ping(context.Context) *goredis.StatusCmd {
	return goredis.NewStatusResult("PONG", nil)
}

func (c *fakeRedisClient) HSet(_ context.Context, key string, values ...any) *goredis.IntCmd {
	c.hsets = append(c.hsets, key)
	if len(values) == 1 {
		if fields, ok := values[0].(map[string]any); ok {
			if c.hashes == nil {
				c.hashes = make(map[string]map[string]string)
			}
			if c.hashes[key] == nil {
				c.hashes[key] = make(map[string]string)
			}
			for field, value := range fields {
				c.hashes[key][field] = fmt.Sprint(value)
			}
		}
	}
	return goredis.NewIntResult(1, nil)
}

func (c *fakeRedisClient) HGet(_ context.Context, key, field string) *goredis.StringCmd {
	if hash, ok := c.hashes[key]; ok {
		if value, ok := hash[field]; ok {
			return goredis.NewStringResult(value, nil)
		}
	}
	return goredis.NewStringResult("", goredis.Nil)
}

func (c *fakeRedisClient) Del(_ context.Context, keys ...string) *goredis.IntCmd {
	c.dels = append(c.dels, append([]string(nil), keys...))
	return goredis.NewIntResult(int64(len(keys)), nil)
}

func (c *fakeRedisClient) Do(ctx context.Context, args ...any) *goredis.Cmd {
	c.doCalls = append(c.doCalls, append([]any(nil), args...))
	cmd := goredis.NewCmd(ctx, args...)
	if c.doFunc != nil {
		result, err := c.doFunc(args)
		if err != nil {
			cmd.SetErr(err)
		} else {
			cmd.SetVal(result)
		}
	} else if c.doResult != nil {
		cmd.SetVal(c.doResult)
	}
	return cmd
}

func (c *fakeRedisClient) Close() error { return nil }

func newTestRedisStore(t *testing.T, client *fakeRedisClient) *Store[redisTestEntry] {
	t.Helper()
	schema, err := parseRedisSchema[redisTestEntry]()
	if err != nil {
		t.Fatal(err)
	}
	return &Store[redisTestEntry]{
		client:   client,
		dim:      1,
		embedder: redisTestEmbedder{},
		schema:   schema,
	}
}

func TestDefaultStoreConfigIsTypeIsolated(t *testing.T) {
	first := defaultStoreConfig[redisTestEntry]()
	second := defaultStoreConfig[anotherRedisTestEntry]()

	if first.indexName == second.indexName || first.keyPrefix == second.keyPrefix {
		t.Fatal("distinct types must use distinct default Redis namespaces")
	}

	WithIndexName("shared-index")(first)
	WithKeyPrefix("shared:")(first)
	if first.indexName != "shared-index" || first.keyPrefix != "shared:" {
		t.Fatal("explicit namespace options must override type-derived defaults")
	}
}

func TestRememberNamespacesKeysByIdentifier(t *testing.T) {
	client := &fakeRedisClient{}
	store := newTestRedisStore(t, client)
	store.keyPrefix = defaultStoreConfig[redisTestEntry]().keyPrefix

	const pk = "shared-pk"
	if err := store.Remember(context.Background(), "tenant/a", redisTestEntry{ID: pk, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Remember(context.Background(), "tenant:b", redisTestEntry{ID: pk, Content: "second"}); err != nil {
		t.Fatal(err)
	}

	if len(client.hsets) != 2 {
		t.Fatalf("expected two writes, got %d", len(client.hsets))
	}
	firstKey := store.keyPrefix + "id:dGVuYW50L2E:" + pk
	secondKey := store.keyPrefix + "id:dGVuYW50OmI:" + pk
	if client.hsets[0] != firstKey || client.hsets[1] != secondKey {
		t.Fatalf("keys = %v, want [%q %q]", client.hsets, firstKey, secondKey)
	}
	if firstKey == secondKey {
		t.Fatal("identifiers sharing a primary key must have distinct Redis keys")
	}

	client.doResult = []interface{}{
		int64(1), firstKey, []interface{}{
			"id", pk,
			"tenant", "tenant/a",
			"content", "first",
			"score", "0",
		},
	}
	entries, err := store.Recall(context.Background(), "tenant/a", memory.RecallQuery{Text: "find first", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != firstKey {
		t.Fatalf("Recall entries = %#v, want storage ID %q", entries, firstKey)
	}

	// Recall returns physical Redis keys as Entry.ID; Update and Forget consume
	// that returned ID unchanged, so the namespaced key remains the mutation target.
	if err := store.Update(context.Background(), "tenant/a", entries[0].ID, redisTestEntry{ID: pk, Content: "updated"}); err != nil {
		t.Fatal(err)
	}
	if len(client.hsets) != 3 || client.hsets[2] != entries[0].ID {
		t.Fatalf("Update writes = %v, want recalled ID %q", client.hsets, entries[0].ID)
	}
	if err := store.Forget(context.Background(), "tenant/a", entries[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(client.dels) != 1 || len(client.dels[0]) != 1 || client.dels[0][0] != entries[0].ID {
		t.Fatalf("Forget deleted %v, want [%q]", client.dels, entries[0].ID)
	}
}

func TestUpdateRejectsForeignTenantEntry(t *testing.T) {
	client := &fakeRedisClient{hashes: map[string]map[string]string{
		"entry-1": {"tenant": "tenant-b"},
	}}
	store := newTestRedisStore(t, client)

	err := store.Update(context.Background(), "tenant-a", "entry-1", redisTestEntry{Content: "replacement"})
	if err == nil {
		t.Fatal("expected foreign entry update to be rejected")
	}
	if len(client.hsets) != 0 {
		t.Fatalf("foreign entry must not be updated; HSET calls: %v", client.hsets)
	}
}

func TestForgetRejectsForeignTenantEntry(t *testing.T) {
	client := &fakeRedisClient{hashes: map[string]map[string]string{
		"entry-1": {"tenant": "tenant-b"},
	}}
	store := newTestRedisStore(t, client)

	err := store.Forget(context.Background(), "tenant-a", "entry-1")
	if err == nil {
		t.Fatal("expected foreign entry deletion to be rejected")
	}
	if len(client.dels) != 0 {
		t.Fatalf("foreign entry must not be deleted; DEL calls: %v", client.dels)
	}
}

func TestCollectPagedKeysReadsAllResults(t *testing.T) {
	const total = 10001
	keys := make([]string, total)
	for i := range keys {
		keys[i] = fmt.Sprintf("entry-%d", i)
	}

	var offsets []int
	got, err := collectPagedKeys(forgetAllPageSize, func(offset, limit int) ([]string, error) {
		offsets = append(offsets, offset)
		end := min(offset+limit, len(keys))
		return keys[offset:end], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Fatalf("expected %d keys, got %d", total, len(got))
	}
	for i, key := range got {
		if key != keys[i] {
			t.Fatalf("key %d = %q, want %q", i, key, keys[i])
		}
	}
	if len(offsets) != 11 || offsets[0] != 0 || offsets[len(offsets)-1] != 10000 {
		t.Fatalf("unexpected pagination offsets: %v", offsets)
	}
}
func TestParseRedisSchemaRejectsPointerGenericType(t *testing.T) {
	if _, err := parseRedisSchema[redisTestEntry](); err != nil {
		t.Fatalf("value type must remain supported: %v", err)
	}

	_, err := parseRedisSchema[*redisTestEntry]()
	if err == nil {
		t.Fatal("expected pointer generic type to be rejected")
	}
	if !strings.Contains(err.Error(), "T must be a non-pointer struct") || !strings.Contains(err.Error(), "*redis.redisTestEntry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateOrValidateIndexAcceptsCompatibleExistingIndex(t *testing.T) {
	schema, err := parseRedisSchema[redisTestEntry]()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &storeConfig{indexName: "memory-index", keyPrefix: "memory:"}
	client := &fakeRedisClient{doFunc: func(args []any) (any, error) {
		switch args[0] {
		case "FT.CREATE":
			return nil, errors.New("Index already exists")
		case "FT.INFO":
			return existingRedisIndexInfo(cfg.keyPrefix, "TEXT", 3), nil
		default:
			t.Fatalf("unexpected Redis command: %v", args)
			return nil, nil
		}
	}}

	if err := createOrValidateIndex(context.Background(), client, cfg, schema, 3); err != nil {
		t.Fatalf("compatible existing index was rejected: %v", err)
	}
	if len(client.doCalls) != 2 || client.doCalls[0][0] != "FT.CREATE" || client.doCalls[1][0] != "FT.INFO" {
		t.Fatalf("commands = %v, want FT.CREATE followed by FT.INFO", client.doCalls)
	}
}

func TestValidateExistingIndexRejectsIncompatibleIndex(t *testing.T) {
	schema, err := parseRedisSchema[redisTestEntry]()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &storeConfig{indexName: "memory-index", keyPrefix: "memory:"}

	cases := []struct {
		name string
		info any
		want string
	}{
		{
			name: "prefix",
			info: existingRedisIndexInfo("other:", "TEXT", 3),
			want: "prefixes",
		},
		{
			name: "field type",
			info: existingRedisIndexInfo("memory:", "TAG", 3),
			want: `field "content" type`,
		},
		{
			name: "vector dimension",
			info: existingRedisIndexInfoRESP3("memory:", "TEXT", "4"),
			want: "embedding DIM",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateExistingIndex(tc.info, cfg, schema, 3)
			if err == nil {
				t.Fatal("expected incompatible index to be rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func existingRedisIndexInfo(prefix, contentType string, dim int) []any {
	return []any{
		"index_definition", []any{"key_type", "HASH", "prefixes", []any{prefix}},
		"attributes", []any{
			[]any{"identifier", "id", "attribute", "id", "type", "TAG"},
			[]any{"identifier", "tenant", "attribute", "tenant", "type", "TAG"},
			[]any{"identifier", "content", "attribute", "content", "type", contentType},
			[]any{"identifier", "embedding", "attribute", "embedding", "type", "VECTOR", "algorithm", "HNSW", "data_type", "FLOAT32", "dim", dim, "distance_metric", "COSINE"},
		},
	}
}

func existingRedisIndexInfoRESP3(prefix, contentType, dim string) map[string]any {
	return map[string]any{
		"index_definition": map[string]any{"key_type": "HASH", "prefixes": []any{prefix}},
		"attributes": []any{
			map[string]any{"identifier": "id", "attribute": "id", "type": "TAG"},
			map[string]any{"identifier": "tenant", "attribute": "tenant", "type": "TAG"},
			map[string]any{"identifier": "content", "attribute": "content", "type": contentType},
			map[string]any{"identifier": "embedding", "attribute": "embedding", "type": "VECTOR", "algorithm": "HNSW", "data_type": "FLOAT32", "dim": dim, "distance_metric": "COSINE"},
		},
	}
}

func TestToolsUseIdentityAndStrictScope(t *testing.T) {
	client := &fakeRedisClient{}
	store := newTestRedisStore(t, client)
	input := []byte(`{"content":"likes Go"}`)

	if _, err := NewRememberTool(store).Handler(agent.Background(), input); !errors.Is(err, memory.ErrMissingIdentity) {
		t.Fatalf("remember without identity: err = %v, want ErrMissingIdentity", err)
	}
	scoped := NewRememberTool(store, WithScope("tenant"))
	if _, err := scoped.Handler(agent.Background().WithIdentity("user-1"), input); !errors.Is(err, memory.ErrMissingIdentity) {
		t.Fatalf("scoped remember must not fall back to identity: err = %v", err)
	}
	if len(client.hsets) != 0 {
		t.Fatalf("writes = %v, want none", client.hsets)
	}

	if _, err := NewRememberTool(store).Handler(agent.Background().WithIdentity("user-1"), input); err != nil {
		t.Fatalf("remember with identity: %v", err)
	}
	if _, err := scoped.Handler(agent.Background().WithScope("tenant", "t-9"), input); err != nil {
		t.Fatalf("scoped remember: %v", err)
	}
	if len(client.hsets) != 2 {
		t.Fatalf("writes = %v, want 2", client.hsets)
	}
	var tenants []string
	for _, h := range client.hashes {
		tenants = append(tenants, h["tenant"])
	}
	if strings.Join(tenants, ",") != "user-1,t-9" && strings.Join(tenants, ",") != "t-9,user-1" {
		t.Errorf("stored tenants = %v, want user-1 and t-9", tenants)
	}

	for name, tl := range map[string]func() (string, error){
		"recall": func() (string, error) {
			return NewRecallTool(store).Handler(agent.Background(), []byte(`{"query":"x"}`))
		},
		"update": func() (string, error) {
			return NewUpdateTool(store).Handler(agent.Background(), []byte(`{"id":"x","content":"y"}`))
		},
		"forget": func() (string, error) { return NewForgetTool(store).Handler(agent.Background(), []byte(`{"id":"x"}`)) },
	} {
		if _, err := tl(); !errors.Is(err, memory.ErrMissingIdentity) {
			t.Errorf("%s without identity: err = %v, want ErrMissingIdentity", name, err)
		}
	}
}

type redisQueryTestEntry struct {
	ID       string    `db:"id,pk"`
	Tenant   string    `db:"tenant,identifier"`
	Content  string    `db:"content,content"`
	Category string    `db:"category,tag"`
	Rank     int       `db:"rank,numeric"`
	Created  time.Time `db:"created,numeric"`
}

func newQueryTestRedisStore(t *testing.T, client *fakeRedisClient) *Store[redisQueryTestEntry] {
	t.Helper()
	schema, err := parseRedisSchema[redisQueryTestEntry]()
	if err != nil {
		t.Fatal(err)
	}
	return &Store[redisQueryTestEntry]{
		client:    client,
		indexName: "memory-index",
		dim:       1,
		embedder:  redisTestEmbedder{},
		schema:    schema,
	}
}

func TestRecallValidatesPortableQuery(t *testing.T) {
	store := newQueryTestRedisStore(t, &fakeRedisClient{})
	cases := []struct {
		name  string
		query memory.RecallQuery
	}{
		{name: "empty text", query: memory.RecallQuery{Limit: 1}},
		{name: "negative limit", query: memory.RecallQuery{Text: "query", Limit: -1}},
		{name: "negative similarity", query: memory.RecallQuery{Text: "query", Limit: 1, MinSimilarity: -0.1}},
		{name: "similarity above one", query: memory.RecallQuery{Text: "query", Limit: 1, MinSimilarity: 1.1}},
		{name: "NaN similarity", query: memory.RecallQuery{Text: "query", Limit: 1, MinSimilarity: math.NaN()}},
		{name: "infinite similarity", query: memory.RecallQuery{Text: "query", Limit: 1, MinSimilarity: math.Inf(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Recall(context.Background(), "tenant", tc.query)
			if !errors.Is(err, memory.ErrInvalidRecallQuery) {
				t.Fatalf("Recall error = %v, want ErrInvalidRecallQuery", err)
			}
		})
	}
}

func TestRecallRejectsUnsupportedPortableFeatures(t *testing.T) {
	store := newQueryTestRedisStore(t, &fakeRedisClient{})
	cases := []struct {
		name  string
		query memory.RecallQuery
		want  error
	}{
		{
			name:  "unknown filter field",
			query: memory.RecallQuery{Text: "query", Limit: 1, Filters: []memory.Filter{{Field: "missing", Operator: memory.FilterEqual, Value: "x"}}},
			want:  memory.ErrUnsupportedFilter,
		},
		{
			name:  "text filter",
			query: memory.RecallQuery{Text: "query", Limit: 1, Filters: []memory.Filter{{Field: "content", Operator: memory.FilterEqual, Value: "x"}}},
			want:  memory.ErrUnsupportedFilter,
		},
		{
			name:  "TAG comparison",
			query: memory.RecallQuery{Text: "query", Limit: 1, Filters: []memory.Filter{{Field: "category", Operator: memory.FilterGreaterThan, Value: "x"}}},
			want:  memory.ErrUnsupportedFilter,
		},
		{
			name:  "TAG non-string",
			query: memory.RecallQuery{Text: "query", Limit: 1, Filters: []memory.Filter{{Field: "category", Operator: memory.FilterEqual, Value: 1}}},
			want:  memory.ErrUnsupportedFilter,
		},
		{
			name:  "NUMERIC non-number",
			query: memory.RecallQuery{Text: "query", Limit: 1, Filters: []memory.Filter{{Field: "rank", Operator: memory.FilterEqual, Value: "1"}}},
			want:  memory.ErrUnsupportedFilter,
		},
		{
			name:  "empty IN",
			query: memory.RecallQuery{Text: "query", Limit: 1, Filters: []memory.Filter{{Field: "rank", Operator: memory.FilterIn, Value: []int{}}}},
			want:  memory.ErrUnsupportedFilter,
		},
		{
			name:  "unknown order field",
			query: memory.RecallQuery{Text: "query", Limit: 1, Order: []memory.Order{{Field: "missing", Direction: memory.OrderAscending}}},
			want:  memory.ErrUnsupportedOrder,
		},
		{
			name:  "non-sortable order field",
			query: memory.RecallQuery{Text: "query", Limit: 1, Order: []memory.Order{{Field: "category", Direction: memory.OrderAscending}}},
			want:  memory.ErrUnsupportedOrder,
		},
		{
			name:  "invalid order direction",
			query: memory.RecallQuery{Text: "query", Limit: 1, Order: []memory.Order{{Field: "rank", Direction: memory.OrderDirection("sideways")}}},
			want:  memory.ErrUnsupportedOrder,
		},
		{
			name:  "multiple order clauses",
			query: memory.RecallQuery{Text: "query", Limit: 1, Order: []memory.Order{{Field: "rank", Direction: memory.OrderAscending}, {Field: "created", Direction: memory.OrderDescending}}},
			want:  memory.ErrUnsupportedOrder,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Recall(context.Background(), "tenant", tc.query)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Recall error = %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}
}

func TestBuildRecallFilterMapsPortableFiltersSafely(t *testing.T) {
	store := newQueryTestRedisStore(t, &fakeRedisClient{})
	filter, err := store.buildRecallFilter(`tenant|other\\name`, []memory.Filter{
		{Field: "category", Operator: memory.FilterIn, Value: []string{"a|b", `c\\d`}},
		{Field: "rank", Operator: memory.FilterGreaterThanOrEqual, Value: 10},
		{Field: "created", Operator: memory.FilterLessThan, Value: time.Unix(20, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantParts := []string{
		`(@tenant:{tenant\|other\\\\name})`,
		`(@category:{a\|b|c\\\\d})`,
		`(@rank:[10 +inf])`,
		`(@created:[-inf (20])`,
	}
	for _, want := range wantParts {
		if !strings.Contains(filter, want) {
			t.Errorf("filter = %q, want safe clause %q", filter, want)
		}
	}
}

func TestRecallUsesPortableOrderWithoutSimilarityResort(t *testing.T) {
	client := &fakeRedisClient{doResult: []any{
		int64(2),
		"first", []any{"tenant", "tenant", "content", "first", "rank", "2", "score", "0.8"},
		"second", []any{"tenant", "tenant", "content", "second", "rank", "1", "score", "0.1"},
	}}
	store := newQueryTestRedisStore(t, client)
	entries, err := store.Recall(context.Background(), "tenant", memory.RecallQuery{
		Text:  "query",
		Limit: 2,
		Order: []memory.Order{{Field: "rank", Direction: memory.OrderDescending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != "first" || entries[1].ID != "second" {
		t.Fatalf("custom ordered entries = %#v, want Redis order preserved", entries)
	}
	args := fmt.Sprint(client.doCalls[0])
	if !strings.Contains(args, "SORTBY rank DESC") || !strings.Contains(args, "(@tenant:{tenant})") {
		t.Fatalf("FT.SEARCH args = %s, want tenant filter and rank DESC", args)
	}
}

func TestRecallLimitZeroCountsMatchesAndReturnsAll(t *testing.T) {
	client := &fakeRedisClient{}
	client.doFunc = func(args []any) (any, error) {
		if len(client.doCalls) == 1 {
			return []any{int64(2)}, nil
		}
		return []any{
			int64(2),
			"first", []any{"tenant", "tenant", "content", "first", "score", "0.2"},
			"second", []any{"tenant", "tenant", "content", "second", "score", "0.1"},
		}, nil
	}
	store := newQueryTestRedisStore(t, client)
	entries, err := store.Recall(context.Background(), "tenant", memory.RecallQuery{Text: "query"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want both matches", entries)
	}
	if len(client.doCalls) != 2 {
		t.Fatalf("Redis calls = %d, want count then search", len(client.doCalls))
	}
	if got := fmt.Sprint(client.doCalls[1]); !strings.Contains(got, "KNN 2") || !strings.Contains(got, "LIMIT 0 2") {
		t.Fatalf("search args = %s, want counted KNN and limit", got)
	}
}

func TestRecallAppliesMinimumSimilarity(t *testing.T) {
	client := &fakeRedisClient{doResult: []any{
		int64(2),
		"low", []any{"tenant", "tenant", "content", "low", "score", "0.6"},
		"high", []any{"tenant", "tenant", "content", "high", "score", "0.1"},
	}}
	store := newQueryTestRedisStore(t, client)
	entries, err := store.Recall(context.Background(), "tenant", memory.RecallQuery{
		Text:          "query",
		Limit:         2,
		MinSimilarity: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "high" {
		t.Fatalf("thresholded entries = %#v, want only high similarity", entries)
	}
}

func TestRecallToolUsesPortableQueryDefaults(t *testing.T) {
	client := &fakeRedisClient{doResult: []any{int64(0)}}
	store := newTestRedisStore(t, client)
	if _, err := NewRecallTool(store).Handler(agent.Background().WithIdentity("tenant"), []byte(`{"query":"find"}`)); err != nil {
		t.Fatal(err)
	}
	if len(client.doCalls) != 1 {
		t.Fatalf("Redis calls = %d, want one recall search", len(client.doCalls))
	}
	args := fmt.Sprint(client.doCalls[0])
	if !strings.Contains(args, "KNN 5") || !strings.Contains(args, "(@tenant:{tenant})") {
		t.Fatalf("FT.SEARCH args = %s, want default limit and identity filter", args)
	}
}
