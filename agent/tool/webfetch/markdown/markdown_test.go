package markdown

import (
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool/webfetch"
)

func TestFormatter_ReturnsWebfetchFormatter(t *testing.T) {
	// Formatter must satisfy the webfetch.Formatter type so it can be passed to
	// webfetch.WithFormatter without an adapter.
	var _ webfetch.Formatter = Formatter()
}

func TestFormatter_ConvertsHTMLToMarkdown(t *testing.T) {
	f := Formatter()
	html := `<h1>Title</h1><p>Hello <a href="https://ex.com">link</a></p><ul><li>one</li><li>two</li></ul>`
	out, err := f(html)
	if err != nil {
		t.Fatalf("formatter error: %v", err)
	}
	checks := []string{
		"# Title",                // heading becomes ATX
		"[link](https://ex.com)", // anchor becomes a markdown link
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
	// List items are preserved (marker style can vary by converter).
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") {
		t.Errorf("list items not preserved:\n%s", out)
	}
}

func TestFormatter_PlainTextPassthrough(t *testing.T) {
	f := Formatter()
	out, err := f("just text, no tags")
	if err != nil {
		t.Fatalf("formatter error: %v", err)
	}
	if !strings.Contains(out, "just text, no tags") {
		t.Errorf("plain text not preserved: %q", out)
	}
}

func TestFormatter_EmptyInput(t *testing.T) {
	f := Formatter()
	out, err := f("")
	if err != nil {
		t.Fatalf("formatter error on empty input: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("empty input should yield empty output, got %q", out)
	}
}
