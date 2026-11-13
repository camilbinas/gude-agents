package tavily

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// rewriteTransport redirects every request to base and records the last one
// (with its body) for assertions.
type rewriteTransport struct {
	base     string
	last     *http.Request
	lastBody []byte
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.last = req.Clone(req.Context())
	var body io.Reader
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		t.lastBody = b
		body = strings.NewReader(string(b))
	}
	target, err := http.NewRequestWithContext(req.Context(), req.Method, t.base, body)
	if err != nil {
		return nil, err
	}
	target.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(target)
}

func callWebSearch(t *testing.T, tl tool.Tool, query string) (string, error) {
	t.Helper()
	in, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	return tl.Handler(context.Background(), in)
}

func TestSearch_ParsesResultsAndAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"42","results":[
			{"title":"Life","url":"https://ex.com/a","content":"the answer","score":0.9}
		]}`))
	}))
	defer srv.Close()

	tl := New("k", WithClient(&http.Client{Transport: &rewriteTransport{base: srv.URL}}), WithIncludeAnswer())
	out, err := callWebSearch(t, tl, "q")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	for _, want := range []string{"Answer: 42", "Life", "https://ex.com/a", "the answer", "0.90"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSearch_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	tl := New("k", WithClient(&http.Client{Transport: &rewriteTransport{base: srv.URL}}))
	out, err := callWebSearch(t, tl, "q")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if !strings.Contains(out, "No results found") {
		t.Errorf("expected no-results message, got %q", out)
	}
}

func TestSearch_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key"))
	}))
	defer srv.Close()

	tl := New("k", WithClient(&http.Client{Transport: &rewriteTransport{base: srv.URL}}))
	_, err := callWebSearch(t, tl, "q")
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should include status code, got %v", err)
	}
	if c := err.Error()[0]; c >= 'A' && c <= 'Z' {
		t.Errorf("error string should not be capitalized: %q", err.Error())
	}
}

func TestSearch_APIKeyOnlyInHeaderAndNeverLogged(t *testing.T) {
	const key = "tv-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	rt := &rewriteTransport{base: srv.URL}
	tl := New(key, WithClient(&http.Client{Transport: rt}))

	var logged strings.Builder
	ctx := tool.WithLogger(context.Background(), tool.FuncLogger(func(msg string) { logged.WriteString(msg) }))
	in, _ := json.Marshal(map[string]string{"query": "weather"})
	if _, err := tl.Handler(ctx, in); err != nil {
		t.Fatalf("search error: %v", err)
	}

	if got := rt.last.Header.Get("Authorization"); got != "Bearer "+key {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer "+key)
	}
	if strings.Contains(string(rt.lastBody), key) {
		t.Errorf("API key leaked into the request body: %s", rt.lastBody)
	}
	if strings.Contains(logged.String(), key) {
		t.Errorf("API key appeared in log output: %q", logged.String())
	}
}

func TestTruncateRunes_Multibyte(t *testing.T) {
	got := truncateRunes("日本語の文", 3)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated string is not valid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, "日本語") || !strings.HasSuffix(got, "…") {
		t.Errorf("want 日本語…, got %q", got)
	}
}

func TestSearch_TruncatesContentAtRuneBoundary(t *testing.T) {
	long := strings.Repeat("え", 400)
	body, _ := json.Marshal(map[string]any{
		"results": []map[string]any{{"title": "t", "url": "u", "content": long, "score": 0.5}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	tl := New("k", WithClient(&http.Client{Transport: &rewriteTransport{base: srv.URL}}),
		WithMaxCharsPerResult(8))
	out, err := callWebSearch(t, tl, "q")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8")
	}
	if !strings.Contains(out, strings.Repeat("え", 8)+"…") {
		t.Errorf("expected 8 runes then ellipsis, got %q", out)
	}
}

func TestWithMaxResults_Clamp(t *testing.T) {
	cases := []struct{ in, want int }{{-1, 1}, {0, 1}, {7, 7}, {20, 20}, {100, 20}}
	for _, c := range cases {
		cfg := &config{}
		WithMaxResults(c.in)(cfg)
		if cfg.maxResults != c.want {
			t.Errorf("WithMaxResults(%d) = %d, want %d", c.in, cfg.maxResults, c.want)
		}
	}
}
