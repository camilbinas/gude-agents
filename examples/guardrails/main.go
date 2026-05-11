// Example: Input and output guardrails.
//
// Input guardrails run before the user message reaches the LLM.
// Output guardrails run after the LLM produces its final response.
// Both can transform content or abort the invocation by returning an error.
//
// By default, output guardrails use stream-through mode: chunks are delivered
// in real-time and guardrails validate the full response at the end. If a
// guardrail rejects the response, a GuardrailError is returned (but the caller
// may have already received partial chunks via InvokeStream).
//
// For buffered mode (chunks held until guardrails pass), see the
// guardrails-buffered example.
//
// Run:
//
//	go run ./guardrails

package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

// blocklist rejects messages containing any of the given words.
func blocklist(words ...string) agent.InputGuardrail {
	return func(_ *agent.Context, msg string) (string, error) {
		lower := strings.ToLower(msg)
		for _, w := range words {
			if strings.Contains(lower, strings.ToLower(w)) {
				return "", fmt.Errorf("message contains blocked term: %q", w)
			}
		}
		return msg, nil
	}
}

// sanitize trims whitespace and collapses internal runs of whitespace.
func sanitize(_ *agent.Context, msg string) (string, error) {
	msg = strings.TrimSpace(msg)
	var b strings.Builder
	prevSpace := false
	for _, r := range msg {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
			}
			prevSpace = true
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return b.String(), nil
}

// redactPII replaces anything that looks like an email address with [REDACTED].
func redactPII(_ *agent.Context, resp string) (string, error) {
	words := strings.Fields(resp)
	for i, w := range words {
		if strings.Contains(w, "@") && strings.Contains(w, ".") {
			words[i] = "[REDACTED]"
		}
	}
	return strings.Join(words, " "), nil
}

// maxLength rejects responses longer than n characters.
func maxLength(n int) agent.OutputGuardrail {
	return func(_ *agent.Context, resp string) (string, error) {
		if len(resp) > n {
			return "", fmt.Errorf("response too long (%d chars, limit %d)", len(resp), n)
		}
		return resp, nil
	}
}

func main() {
	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.Default(provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		// Input guardrails — run in order before the LLM sees the message.
		agent.WithInputGuardrail(sanitize),
		agent.WithInputGuardrail(blocklist("confidential", "password", "secret")),
		// Output guardrails — run in order on the final response.
		// Chunks stream in real-time; guardrails validate after completion.
		agent.WithOutputGuardrail(redactPII),
		agent.WithOutputGuardrail(maxLength(2000)),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background()

	// ── Stream-through: chunks arrive in real-time ────────────────────
	fmt.Println("── Streaming with output guardrails (stream-through) ──")
	err = a.InvokeStream(ctx, "What is the capital of France?", func(chunk string) {
		fmt.Print(chunk) // chunks arrive immediately, not buffered
	})
	fmt.Println()
	if err != nil {
		var ge *agent.GuardrailError
		if errors.As(err, &ge) {
			fmt.Println("Guardrail rejected:", ge)
		} else {
			log.Fatal(err)
		}
	}

	// ── Invoke (non-streaming): returns guardrail-processed text ──────
	fmt.Println("\n── Invoke returns guardrail-processed text ──")
	result, err := a.Invoke(ctx, "  What is the capital of France?  ")
	if err != nil {
		log.Fatalf("unexpected error: %v", err)
	}
	fmt.Println("Response:", result)

	// ── Blocked message — input guardrail rejects it ──────────────────
	_, err = a.Invoke(ctx, "What is the password for the admin account?")
	if err != nil {
		fmt.Println("Blocked:", err)
	}
}

// blockedError is an example of a typed error for programmatic handling.
// Replace the blocklist function above with this version to use it.
type blockedError struct {
	Term string
}

func (e *blockedError) Error() string {
	return fmt.Sprintf("message contains blocked term: %q", e.Term)
}
