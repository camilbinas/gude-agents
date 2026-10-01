package redis

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/memory"
)

type redisDecodeEntry struct {
	ID      string         `db:"id,pk"`
	Tenant  string         `db:"tenant,identifier"`
	Content string         `db:"content,content"`
	Small   int32          `db:"small,numeric"`
	Count   uint16         `db:"count,numeric"`
	Ratio   float32        `db:"ratio,numeric"`
	Big     float64        `db:"big,numeric"`
	Created time.Time      `db:"created,numeric"`
	Flag    bool           `db:"flag"`
	Meta    map[string]int `db:"meta,jsonb"`
}

func newTypedTestStore[T any](t *testing.T, client *fakeRedisClient) *Store[T] {
	t.Helper()
	schema, err := parseRedisSchema[T]()
	if err != nil {
		t.Fatal(err)
	}
	return &Store[T]{
		client:    client,
		indexName: "memory-index",
		keyPrefix: "mem:",
		dim:       1,
		embedder:  redisTestEmbedder{},
		schema:    schema,
	}
}

// validDecodeAttrs returns a RESP2 field list for a fully populated entry.
// Overrides replace the value of an existing field; a nil override removes it.
func validDecodeAttrs(overrides map[string]any) []any {
	base := [][2]any{
		{"id", "pk-1"},
		{"tenant", "tenant"},
		{"content", "hello"},
		{"small", "-42"},
		{"count", "65535"},
		{"ratio", "0.5"},
		{"big", "12345.678"},
		{"created", "1700000000"},
		{"flag", "true"},
		{"meta", `{"a":1}`},
		{"score", "0.25"},
	}
	var out []any
	for _, kv := range base {
		name := kv[0].(string)
		value := kv[1]
		if override, ok := overrides[name]; ok {
			if override == nil {
				continue
			}
			value = override
		}
		out = append(out, name, value)
	}
	return out
}

func wantDecodeEntry() redisDecodeEntry {
	return redisDecodeEntry{
		ID:      "pk-1",
		Tenant:  "tenant",
		Content: "hello",
		Small:   -42,
		Count:   65535,
		Ratio:   0.5,
		Big:     12345.678,
		Created: time.Unix(1700000000, 0),
		Flag:    true,
		Meta:    map[string]int{"a": 1},
	}
}

func assertDecodedEntry(t *testing.T, entries []memory.Entry[redisDecodeEntry]) {
	t.Helper()
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one", entries)
	}
	got := entries[0]
	if got.ID != "mem:doc-1" {
		t.Fatalf("ID = %q, want mem:doc-1", got.ID)
	}
	if got.Score != 0.75 {
		t.Fatalf("Score = %v, want 0.75 (1 - distance)", got.Score)
	}
	want := wantDecodeEntry()
	if !got.Value.Created.Equal(want.Created) {
		t.Fatalf("Created = %v, want %v", got.Value.Created, want.Created)
	}
	got.Value.Created, want.Created = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got.Value, want) {
		t.Fatalf("Value = %#v, want %#v", got.Value, want)
	}
}

func resp2Attrs(attrs []any) map[string]any {
	out := make(map[string]any, len(attrs)/2)
	for i := 0; i+1 < len(attrs); i += 2 {
		out[attrs[i].(string)] = attrs[i+1]
	}
	return out
}

func TestParseResultsDecodesValidRESP2(t *testing.T) {
	store := newTypedTestStore[redisDecodeEntry](t, &fakeRedisClient{})
	entries, err := store.parseResults([]any{int64(1), "mem:doc-1", validDecodeAttrs(nil)}, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	assertDecodedEntry(t, entries)
}

func TestParseResultsDecodesValidRESP3(t *testing.T) {
	store := newTypedTestStore[redisDecodeEntry](t, &fakeRedisClient{})
	attrs := resp2Attrs(validDecodeAttrs(nil))

	t.Run("map[any]any", func(t *testing.T) {
		anyAttrs := make(map[any]any, len(attrs))
		for k, v := range attrs {
			anyAttrs[k] = v
		}
		res := map[any]any{
			"total_results": int64(1),
			"results": []any{
				map[any]any{"id": "mem:doc-1", "extra_attributes": anyAttrs, "values": []any{}},
			},
		}
		entries, err := store.parseResults(res, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		assertDecodedEntry(t, entries)
	})

	t.Run("map[string]any", func(t *testing.T) {
		res := map[string]any{
			"total_results": int64(1),
			"results": []any{
				map[string]any{"id": "mem:doc-1", "extra_attributes": attrs},
			},
		}
		entries, err := store.parseResults(res, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		assertDecodedEntry(t, entries)
	})
}

func TestParseResultsEmptyResults(t *testing.T) {
	store := newTypedTestStore[redisDecodeEntry](t, &fakeRedisClient{})
	cases := map[string]any{
		"RESP2":             []any{int64(0)},
		"RESP3 map[any]any": map[any]any{"total_results": int64(0), "results": []any{}},
		"RESP3 map[string]": map[string]any{"total_results": int64(0), "results": []any{}},
	}
	for name, res := range cases {
		t.Run(name, func(t *testing.T) {
			entries, err := store.parseResults(res, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			if entries == nil || len(entries) != 0 {
				t.Fatalf("entries = %#v, want non-nil empty slice", entries)
			}
		})
	}
}

func TestParseResultsRejectsMalformedFieldValues(t *testing.T) {
	store := newTypedTestStore[redisDecodeEntry](t, &fakeRedisClient{})
	cases := []struct {
		name  string
		field string
		value any
	}{
		{"malformed JSON", "meta", `{"a":`},
		{"JSON type mismatch", "meta", `{"a":"x"}`},
		{"invalid signed integer", "small", "abc"},
		{"fractional signed integer", "small", "1.5"},
		{"int32 overflow", "small", "2147483648"},
		{"huge integer overflow", "small", "999999999999999999999"},
		{"negative unsigned", "count", "-1"},
		{"uint16 overflow", "count", "65536"},
		{"invalid float", "big", "twelve"},
		{"NaN float", "big", "NaN"},
		{"infinite float", "big", "+Inf"},
		{"float32 overflow", "ratio", "1e39"},
		{"invalid time", "created", "2024-01-01T00:00:00Z"},
		{"invalid bool", "flag", "maybe"},
		{"aggregate in string field", "content", []any{"nested"}},
		{"nil value", "tenant", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := validDecodeAttrs(nil)
			for i := 0; i+1 < len(attrs); i += 2 {
				if attrs[i] == tc.field {
					attrs[i+1] = tc.value
				}
			}
			entries, err := store.parseResults([]any{int64(1), "mem:doc-1", attrs}, 0, true)
			if err == nil {
				t.Fatalf("parseResults succeeded with entries %#v, want error", entries)
			}
			if entries != nil {
				t.Fatalf("entries = %#v, want nil on error", entries)
			}
			if !strings.Contains(err.Error(), `decode field "`+tc.field+`"`) {
				t.Fatalf("error = %v, want field %q named", err, tc.field)
			}
		})
	}
}

func TestParseResultsRejectsMalformedResponses(t *testing.T) {
	store := newTypedTestStore[redisDecodeEntry](t, &fakeRedisClient{})
	valid := validDecodeAttrs(nil)
	validMap := resp2Attrs(valid)
	cases := []struct {
		name string
		res  any
		want string
	}{
		{"malformed score", []any{int64(1), "mem:doc-1", validDecodeAttrs(map[string]any{"score": "close"})}, "invalid score"},
		{"NaN score", []any{int64(1), "mem:doc-1", validDecodeAttrs(map[string]any{"score": "NaN"})}, "invalid score"},
		{"non-scalar score", []any{int64(1), "mem:doc-1", validDecodeAttrs(map[string]any{"score": []any{}})}, "score is"},
		{"missing score", []any{int64(1), "mem:doc-1", validDecodeAttrs(map[string]any{"score": nil})}, "no score"},
		{"RESP2 non-string id", []any{int64(1), int64(7), valid}, "document id"},
		{"RESP2 empty id", []any{int64(1), "", valid}, "document id is empty"},
		{"RESP2 attributes not array", []any{int64(1), "mem:doc-1", "content"}, "want array"},
		{"RESP2 odd attribute list", []any{int64(1), "mem:doc-1", []any{"content"}}, "odd length"},
		{"RESP2 non-string attribute name", []any{int64(1), "mem:doc-1", []any{[]any{}, "x"}}, "attribute name"},
		{"RESP2 key without attributes", []any{int64(1), "mem:doc-1"}, "key/attribute pairs"},
		{"RESP2 empty array", []any{}, "result count"},
		{"RESP2 non-integer count", []any{"many", "mem:doc-1", valid}, "result count"},
		{"RESP3 missing results", map[any]any{"total_results": int64(1)}, "no results"},
		{"RESP3 results not array", map[any]any{"results": "x"}, "want array"},
		{"RESP3 result not map", map[any]any{"results": []any{"x"}}, "want map"},
		{"RESP3 missing id", map[any]any{"results": []any{map[any]any{"extra_attributes": validMap}}}, "document id"},
		{"RESP3 non-string id", map[any]any{"results": []any{map[any]any{"id": int64(1), "extra_attributes": validMap}}}, "document id"},
		{"RESP3 missing attributes", map[any]any{"results": []any{map[any]any{"id": "mem:doc-1"}}}, "no extra_attributes"},
		{"RESP3 attributes not map", map[any]any{"results": []any{map[any]any{"id": "mem:doc-1", "extra_attributes": []any{}}}}, "want map"},
		{"unexpected response type", "OK", "unexpected response type"},
		{"nil response", nil, "unexpected response type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := store.parseResults(tc.res, 0, true)
			if err == nil {
				t.Fatalf("parseResults succeeded with entries %#v, want error", entries)
			}
			if entries != nil {
				t.Fatalf("entries = %#v, want nil on error", entries)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A single corrupted entry must fail the whole Recall rather than returning
// the valid entries alongside a zero-valued one.
func TestRecallFailsWhenAnyEntryIsCorrupted(t *testing.T) {
	client := &fakeRedisClient{doResult: []any{
		int64(2),
		"mem:doc-1", validDecodeAttrs(nil),
		"mem:doc-2", validDecodeAttrs(map[string]any{"small": "not-a-number"}),
	}}
	store := newTypedTestStore[redisDecodeEntry](t, client)
	entries, err := store.Recall(context.Background(), "tenant", memory.RecallQuery{Text: "q", Limit: 2})
	if err == nil {
		t.Fatalf("Recall succeeded with %#v, want decode error", entries)
	}
	if entries != nil {
		t.Fatalf("entries = %#v, want nil on error", entries)
	}
	if !strings.Contains(err.Error(), `entry "mem:doc-2"`) {
		t.Fatalf("error = %v, want corrupted entry identified", err)
	}
}

func TestMissingSchemaFieldIsTolerated(t *testing.T) {
	store := newTypedTestStore[redisDecodeEntry](t, &fakeRedisClient{})
	entries, err := store.parseResults([]any{int64(1), "mem:doc-1", validDecodeAttrs(map[string]any{"small": nil})}, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Value.Small != 0 || entries[0].Value.Content != "hello" {
		t.Fatalf("entries = %#v, want absent field left at zero and others decoded", entries)
	}
}

func TestRememberRecallRoundTripsTypedFields(t *testing.T) {
	client := &fakeRedisClient{}
	store := newTypedTestStore[redisDecodeEntry](t, client)
	value := redisDecodeEntry{
		ID:      "pk-1",
		Content: "hello",
		Small:   math.MinInt32,
		Count:   math.MaxUint16,
		Ratio:   float32(0.1),
		Big:     1e21,
		Created: time.Unix(1700000000, 0),
		Flag:    true,
		Meta:    map[string]int{"a": 1},
	}
	if err := store.Remember(context.Background(), "tenant", value); err != nil {
		t.Fatal(err)
	}
	key := client.hsets[0]
	var attrs []any
	for name, stored := range client.hashes[key] {
		if name == "embedding" {
			continue
		}
		attrs = append(attrs, name, stored)
	}
	attrs = append(attrs, "score", "0")
	client.doResult = []any{int64(1), key, attrs}

	entries, err := store.Recall(context.Background(), "tenant", memory.RecallQuery{Text: "q", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one", entries)
	}
	got := entries[0].Value
	value.Tenant = "tenant"
	if !got.Created.Equal(value.Created) {
		t.Fatalf("Created = %v, want %v", got.Created, value.Created)
	}
	got.Created, value.Created = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, value) {
		t.Fatalf("round trip = %#v, want %#v", got, value)
	}
}

func TestRememberRejectsUnencodableValues(t *testing.T) {
	type jsonEntry struct {
		ID      string `db:"id,pk"`
		Tenant  string `db:"tenant,identifier"`
		Content string `db:"content,content"`
		Payload any    `db:"payload,jsonb"`
	}

	t.Run("non-finite numeric", func(t *testing.T) {
		client := &fakeRedisClient{}
		store := newTypedTestStore[redisDecodeEntry](t, client)
		err := store.Remember(context.Background(), "tenant", redisDecodeEntry{Content: "x", Big: math.Inf(1)})
		if err == nil || !strings.Contains(err.Error(), `encode field "big"`) {
			t.Fatalf("Remember error = %v, want encode error for big", err)
		}
		if len(client.hsets) != 0 {
			t.Fatalf("HSET calls = %v, want none", client.hsets)
		}
	})

	t.Run("JSON marshal failure", func(t *testing.T) {
		client := &fakeRedisClient{}
		store := newTypedTestStore[jsonEntry](t, client)
		err := store.Remember(context.Background(), "tenant", jsonEntry{Content: "x", Payload: make(chan int)})
		if err == nil || !strings.Contains(err.Error(), `encode field "payload"`) {
			t.Fatalf("Remember error = %v, want encode error for payload", err)
		}
		if len(client.hsets) != 0 {
			t.Fatalf("HSET calls = %v, want none", client.hsets)
		}
	})
}

func TestParseRedisSchemaRejectsUnroundtrippableFields(t *testing.T) {
	type sliceField struct {
		Tenant  string   `db:"tenant,identifier"`
		Content string   `db:"content,content"`
		Tags    []string `db:"tags"`
	}
	type pointerField struct {
		Tenant  string `db:"tenant,identifier"`
		Content string `db:"content,content"`
		Rank    *int   `db:"rank,numeric"`
	}
	type structField struct {
		Tenant  string          `db:"tenant,identifier"`
		Content string          `db:"content,content"`
		Inner   struct{ A int } `db:"inner"`
	}
	type numericString struct {
		Tenant  string `db:"tenant,identifier"`
		Content string `db:"content,content"`
		Rank    string `db:"rank,numeric"`
	}
	type taggedTime struct {
		Tenant  string    `db:"tenant,identifier"`
		Content string    `db:"content,content"`
		At      time.Time `db:"at,tag"`
	}
	type nonStringContent struct {
		Tenant  string `db:"tenant,identifier"`
		Content int    `db:"content,content"`
	}
	type jsonbSlice struct {
		Tenant  string   `db:"tenant,identifier"`
		Content string   `db:"content,content"`
		Tags    []string `db:"tags,jsonb"`
	}

	rejects := map[string]func() error{
		"slice":              func() error { _, err := parseRedisSchema[sliceField](); return err },
		"pointer":            func() error { _, err := parseRedisSchema[pointerField](); return err },
		"struct":             func() error { _, err := parseRedisSchema[structField](); return err },
		"numeric string":     func() error { _, err := parseRedisSchema[numericString](); return err },
		"tag time":           func() error { _, err := parseRedisSchema[taggedTime](); return err },
		"non-string content": func() error { _, err := parseRedisSchema[nonStringContent](); return err },
	}
	for name, parse := range rejects {
		t.Run(name, func(t *testing.T) {
			if err := parse(); err == nil {
				t.Fatal("parseRedisSchema succeeded, want unsupported field error")
			}
		})
	}
	if _, err := parseRedisSchema[jsonbSlice](); err != nil {
		t.Fatalf("jsonb slice rejected: %v", err)
	}
}

type failingReader struct{}

var errTestRNG = errors.New("rng unavailable")

func (failingReader) Read([]byte) (int, error) { return 0, errTestRNG }

// Generating a primary key must surface RNG failures (uuid.New would panic)
// and must not write anything.
func TestRememberPropagatesPrimaryKeyRNGFailure(t *testing.T) {
	client := &fakeRedisClient{}
	store := newTestRedisStore(t, client)
	store.random = failingReader{}

	err := store.Remember(context.Background(), "tenant", redisTestEntry{Content: "x"})
	if !errors.Is(err, errTestRNG) {
		t.Fatalf("Remember error = %v, want RNG failure", err)
	}
	if len(client.hsets) != 0 {
		t.Fatalf("HSET calls = %v, want none", client.hsets)
	}
	if err := store.Remember(context.Background(), "tenant", redisTestEntry{ID: "fixed", Content: "x"}); err != nil {
		t.Fatalf("Remember with explicit ID: %v", err)
	}
}

func sf(name string, typ any, tag string) reflect.StructField {
	return reflect.StructField{Name: name, Type: reflect.TypeOf(typ), Tag: reflect.StructTag(`db:"` + tag + `"`)}
}

var (
	baseIdent   = sf("Tenant", "", "tenant,identifier")
	baseContent = sf("Content", "", "content,content")
)

func TestParseRedisSchemaRejectsInvalidTagCombinations(t *testing.T) {
	cases := []struct {
		name   string
		fields []reflect.StructField
	}{
		{"identifier+jsonb", []reflect.StructField{sf("Tenant", "", "tenant,identifier,jsonb"), baseContent}},
		{"jsonb+identifier", []reflect.StructField{sf("Tenant", "", "tenant,jsonb,identifier"), baseContent}},
		{"content+jsonb", []reflect.StructField{baseIdent, sf("Content", "", "content,content,jsonb")}},
		{"jsonb+content", []reflect.StructField{baseIdent, sf("Content", "", "content,jsonb,content")}},
		{"content+jsonb non-string", []reflect.StructField{baseIdent, sf("Content", []string{}, "content,content,jsonb")}},
		{"pk+jsonb", []reflect.StructField{sf("ID", "", "id,pk,jsonb"), baseIdent, baseContent}},
		{"jsonb+numeric", []reflect.StructField{baseIdent, baseContent, sf("Rank", 0.0, "rank,jsonb,numeric")}},
		{"numeric+jsonb", []reflect.StructField{baseIdent, baseContent, sf("Rank", 0.0, "rank,numeric,jsonb")}},
		{"jsonb+tag", []reflect.StructField{baseIdent, baseContent, sf("Tags", []string{}, "tags,jsonb,tag")}},
		{"tag+jsonb", []reflect.StructField{baseIdent, baseContent, sf("Tags", []string{}, "tags,tag,jsonb")}},
		{"tag+numeric", []reflect.StructField{baseIdent, baseContent, sf("Rank", 0, "rank,tag,numeric")}},
		{"identifier+numeric", []reflect.StructField{sf("Tenant", "", "tenant,identifier,numeric"), baseContent}},
		{"content+tag", []reflect.StructField{baseIdent, sf("Content", "", "content,content,tag")}},
		{"content+numeric", []reflect.StructField{baseIdent, sf("Content", "", "content,content,numeric")}},
		{"identifier+content", []reflect.StructField{sf("Tenant", "", "tenant,identifier,content")}},
		{"pk+identifier", []reflect.StructField{sf("Tenant", "", "tenant,pk,identifier"), baseContent}},
		{"non-string identifier", []reflect.StructField{sf("Tenant", 0, "tenant,identifier"), baseContent}},
		{"non-string content", []reflect.StructField{baseIdent, sf("Content", 0, "content,content")}},
		{"duplicate identifier", []reflect.StructField{baseIdent, sf("Other", "", "other,identifier"), baseContent}},
		{"duplicate content", []reflect.StructField{baseIdent, baseContent, sf("Body", "", "body,content")}},
		{"duplicate pk", []reflect.StructField{sf("ID", "", "id,pk"), sf("Key", "", "key,pk"), baseIdent, baseContent}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := parseRedisSchemaType(reflect.StructOf(tc.fields))
			if err == nil {
				t.Fatalf("parseRedisSchemaType succeeded with %+v, want error", schema.Fields)
			}
		})
	}
}

func TestParseRedisSchemaAcceptsValidTagCombinations(t *testing.T) {
	cases := []struct {
		name      string
		field     reflect.StructField
		wantType  redisFieldType
		wantJSONB bool
	}{
		{"jsonb slice", sf("Tags", []string{}, "tags,jsonb"), fieldTEXT, true},
		{"jsonb map", sf("Meta", map[string]int{}, "meta,jsonb"), fieldTEXT, true},
		{"jsonb struct", sf("Inner", struct{ A int }{}, "inner,jsonb"), fieldTEXT, true},
		{"jsonb pointer", sf("Ptr", (*int)(nil), "ptr,jsonb"), fieldTEXT, true},
		{"jsonb string", sf("Raw", "", "raw,jsonb"), fieldTEXT, true},
		{"jsonb noinput", sf("Audit", []string{}, "audit,jsonb,noinput"), fieldTEXT, true},
		{"explicit numeric", sf("Rank", 0, "rank,numeric"), fieldNUMERIC, false},
		{"inferred numeric", sf("Rank", 0, "rank"), fieldNUMERIC, false},
		{"tag on int", sf("Code", 0, "code,tag"), fieldTAG, false},
		{"time numeric", sf("At", time.Time{}, "at"), fieldNUMERIC, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := parseRedisSchemaType(reflect.StructOf([]reflect.StructField{baseIdent, baseContent, tc.field}))
			if err != nil {
				t.Fatal(err)
			}
			got := schema.Fields[2]
			if got.FieldType != tc.wantType || got.IsJSONB != tc.wantJSONB {
				t.Fatalf("field = %+v, want type %v jsonb %v", got, tc.wantType, tc.wantJSONB)
			}
		})
	}

	// Roles keep their required Redis types regardless of modifier order.
	schema, err := parseRedisSchemaType(reflect.StructOf([]reflect.StructField{
		sf("ID", "", "id,pk"), sf("Tenant", "", "tenant,tag,identifier"), baseContent,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if f := schema.Fields[schema.IdentifierIdx]; f.FieldType != fieldTAG || !f.NoInput {
		t.Fatalf("identifier = %+v, want TAG and noinput", f)
	}
	if f := schema.Fields[schema.ContentIdx]; f.FieldType != fieldTEXT {
		t.Fatalf("content = %+v, want TEXT", f)
	}
	if f := schema.Fields[schema.PKIdx]; !f.NoInput || f.IsJSONB {
		t.Fatalf("pk = %+v, want noinput, not jsonb", f)
	}
}
