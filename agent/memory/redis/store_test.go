package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
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

var _ agent.Embedder = redisTestEmbedder{}

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
	entries, err := store.Recall(context.Background(), "tenant/a", "find first", 1)
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
