package brave

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// rewriteTransport sends every request to base instead of its real host, so a
// tool that targets a fixed public URL can be exercised against an httptest
// server. It records the last request for header assertions.
type rewriteTransport struct {
	base string
	last *http.Request
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.last = req.Clone(req.Context())
	target, err := http.NewRequestWithContext(req.Context(), req.Method, t.base+"?"+req.URL.RawQuery, req.Body)
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

func TestSearch_ParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[
			{"title":"Go","url":"https://go.dev","description":"The Go programming language"},
			{"title":"Rust","url":"https://rust-lang.org","description":"A systems language"}
		]}}`))
	}))
	defer srv.Close()

	rt := &rewriteTransport{base: srv.URL}
	tl := New("secret-key", WithClient(&http.Client{Transport: rt}))
	out, err := callWebSearch(t, tl, "languages")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	for _, want := range []string{"Go", "https://go.dev", "The Go programming language", "Rust"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSearch_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	tl := New("k", WithClient(&http.Client{Transport: &rewriteTransport{base: srv.URL}}))
	out, err := callWebSearch(t, tl, "nothing")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if !strings.Contains(out, "No results found") {
		t.Errorf("expected no-results message, got %q", out)
	}
}

func TestSearch_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	tl := New("k", WithClient(&http.Client{Transport: &rewriteTransport{base: srv.URL}}))
	_, err := callWebSearch(t, tl, "q")
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should include status code, got %v", err)
	}
	// ST1005: error strings must not start with a capital letter.
	if c := err.Error()[0]; c >= 'A' && c <= 'Z' {
		t.Errorf("error string should not be capitalized: %q", err.Error())
	}
}

func TestSearch_APIKeyOnlyInHeaderAndNeverLogged(t *testing.T) {
	const key = "super-secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
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

	// The key must travel only in the X-Subscription-Token header.
	if got := rt.last.Header.Get("X-Subscription-Token"); got != key {
		t.Errorf("API key header = %q, want %q", got, key)
	}
	if strings.Contains(rt.last.URL.RawQuery, key) {
		t.Errorf("API key leaked into the request URL: %s", rt.last.URL.RawQuery)
	}
	if strings.Contains(logged.String(), key) {
		t.Errorf("API key appeared in log output: %q", logged.String())
	}
}

func TestTruncateRunes_Multibyte(t *testing.T) {
	// 5 multibyte runes; truncating to 3 must keep valid UTF-8.
	s := "日本語の文"
	got := truncateRunes(s, 3)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated string is not valid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, "日本語") {
		t.Errorf("want prefix 日本語, got %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); n != 3 {
		t.Errorf("want 3 runes before ellipsis, got %d", n)
	}
}

func TestTruncateRunes_NoCut(t *testing.T) {
	if got := truncateRunes("abc", 5); got != "abc" {
		t.Errorf("want unchanged, got %q", got)
	}
	if got := truncateRunes("abc", 0); got != "abc" {
		t.Errorf("non-positive max should disable truncation, got %q", got)
	}
}

func TestSearch_TruncatesSnippetAtRuneBoundary(t *testing.T) {
	long := strings.Repeat("あ", 500) // 500 multibyte runes
	body, _ := json.Marshal(map[string]any{
		"web": map[string]any{
			"results": []map[string]string{{"title": "t", "url": "u", "description": long}},
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	tl := New("k", WithClient(&http.Client{Transport: &rewriteTransport{base: srv.URL}}),
		WithMaxCharsPerResult(10))
	out, err := callWebSearch(t, tl, "q")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8")
	}
	if !strings.Contains(out, strings.Repeat("あ", 10)+"…") {
		t.Errorf("expected 10 runes then ellipsis, got %q", out)
	}
}

func TestWithMaxResults_Clamp(t *testing.T) {
	cases := []struct{ in, want int }{{-5, 1}, {0, 1}, {3, 3}, {20, 20}, {999, 20}}
	for _, c := range cases {
		cfg := &config{}
		WithMaxResults(c.in)(cfg)
		if cfg.maxResults != c.want {
			t.Errorf("WithMaxResults(%d) = %d, want %d", c.in, cfg.maxResults, c.want)
		}
	}
}
