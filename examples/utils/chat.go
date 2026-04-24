package utils

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
)

// ChatOptions configures the interactive chat loop.
type ChatOptions struct {
	// ClearFunc is called when the user types "clear". If nil, clear is not supported.
	ClearFunc func(ctx context.Context) error
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

		_, err := a.InvokeStream(ctx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		fmt.Println()

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

// ClearMemory returns a ClearFunc that deletes a conversation from a MemoryManager.
// Use with ChatOptions.ClearFunc for memory-backed examples.
func ClearMemory(m memory.MemoryManager, conversationID string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return m.Delete(ctx, conversationID)
	}
}
