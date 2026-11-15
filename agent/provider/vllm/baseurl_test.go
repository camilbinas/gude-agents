package vllm

import "testing"

// All of these forms must collapse to exactly one /v1, never /v1/v1.
func TestNormalizeBaseURL(t *testing.T) {
	const want = "http://host:8000/v1"
	for _, in := range []string{
		"http://host:8000",
		"http://host:8000/",
		"http://host:8000/v1",
		"http://host:8000/v1/",
	} {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
