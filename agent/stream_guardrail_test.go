package agent

import (
	"errors"
	"strings"
	"testing"
)

func streamedText(t *testing.T, a *Runtime) ([]string, error) {
	t.Helper()
	var chunks []string
	var gotErr error
	for chunk, err := range a.TextStream(Background(), "q") {
		if err != nil {
			gotErr = err
			continue
		}
		chunks = append(chunks, chunk)
	}
	return chunks, gotErr
}

func TestStream_RejectingOutputGuardrailStreamsNoText(t *testing.T) {
	blocked := errors.New("blocked")
	a, err := New(newScriptedProvider(&ModelResponse{Text: "secret answer here"}), "sys",
		WithOutputGuardrail(func(_ *Context, _ string) (string, error) { return "", blocked }))
	if err != nil {
		t.Fatal(err)
	}
	chunks, gotErr := streamedText(t, a)
	if len(chunks) != 0 {
		t.Fatalf("streamed %q before the guardrail rejected the answer", chunks)
	}
	var ge *GuardrailError
	if !errors.As(gotErr, &ge) || !errors.Is(gotErr, blocked) {
		t.Fatalf("err = %v, want GuardrailError wrapping blocked", gotErr)
	}
}

func TestStream_RewritingOutputGuardrailStreamsOnlyTheRewrite(t *testing.T) {
	a, err := New(newScriptedProvider(&ModelResponse{Text: "raw model words"}), "sys",
		WithOutputGuardrail(func(_ *Context, s string) (string, error) { return strings.ToUpper(s), nil }))
	if err != nil {
		t.Fatal(err)
	}
	chunks, gotErr := streamedText(t, a)
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if len(chunks) != 1 || chunks[0] != "RAW MODEL WORDS" {
		t.Fatalf("chunks = %q, want one rewritten chunk", chunks)
	}
}

func TestStream_WithoutGuardrailsStillStreamsLive(t *testing.T) {
	a, err := New(newScriptedProvider(&ModelResponse{Text: "one two three"}), "sys")
	if err != nil {
		t.Fatal(err)
	}
	chunks, gotErr := streamedText(t, a)
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if len(chunks) < 3 || strings.Join(chunks, "") != "one two three" {
		t.Fatalf("chunks = %q, want live word chunks", chunks)
	}
}
