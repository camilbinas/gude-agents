package utils

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

// ChatOptions configures the interactive chat loop.
type ChatOptions struct {
	// ClearFunc is called when the user types "clear". If nil, clear is not supported.
	ClearFunc func(ctx context.Context) error

	// BeforeInvoke is called before each agent invocation with the user input.
	// Use it to set up per-invocation context (e.g. override inference config).
	// If it returns a non-nil context, that context is used for the invocation.
	BeforeInvoke func(ctx context.Context, input string) context.Context

	// AfterInvoke is called after each agent invocation with the usage and error.
	// Use it for post-invocation work like flushing trace exporters or logging tokens.
	AfterInvoke func(ctx context.Context, usage agent.TokenUsage, err error)
}

// Chat runs an interactive chat loop with the given agent. It reads from stdin,
// streams responses to stdout, and supports "quit" (exit) and optionally "clear"
// (reset conversation). Use this in examples to avoid duplicating the interactive
// loop boilerplate.
func Chat(ctx context.Context, a *agent.Agent, opts ...ChatOptions) {
	var o ChatOptions
	if len(opts) > 0 {
		o = opts[0]
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "quit") || strings.EqualFold(input, "exit") {
			break
		}
		if strings.EqualFold(input, "clear") && o.ClearFunc != nil {
			if err := o.ClearFunc(ctx); err != nil {
				fmt.Printf("Error clearing: %v\n", err)
			} else {
				fmt.Println("Conversation cleared.")
			}
			continue
		}

		invokeCtx := ctx
		if o.BeforeInvoke != nil {
			if c := o.BeforeInvoke(ctx, input); c != nil {
				invokeCtx = c
			}
		}

		usage, err := a.InvokeStream(invokeCtx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		fmt.Println()

		if o.AfterInvoke != nil {
			o.AfterInvoke(ctx, usage, err)
		}

		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
	}
}

// SwarmChat runs an interactive chat loop with a swarm. Same UX as Chat but
// also prints which agent handled the request and handoff count.
func SwarmChat(ctx context.Context, sw *agent.Swarm) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "quit") || strings.EqualFold(input, "exit") {
			break
		}

		_, err := sw.Run(ctx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			continue
		}
	}
}

// ClearConversation returns a ClearFunc that deletes a conversation.
func ClearConversation(m conversation.ConversationManager, conversationID string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return m.Delete(ctx, conversationID)
	}
}
