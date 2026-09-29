package utils

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
)

// ChatOptions configures the interactive chat loop.
type ChatOptions struct {
	// ClearFunc is called when the user types "clear". If nil, clear is not supported.
	ClearFunc func(ctx context.Context) error

	// BeforeInvoke is called before each agent invocation with the user input.
	// Use it to set up per-invocation context (e.g. override inference config).
	// If it returns a non-nil context, that context is used for the invocation.
	BeforeInvoke func(c *agent.Context, input string) *agent.Context

	// AfterInvoke is called after each invocation with its canonical Result and
	// error (if any). Use Result.Usage for token accounting.
	AfterInvoke func(c *agent.Context, result agent.Result, err error)
}

// Chat runs an interactive chat session with the given agent.
//
// By default it launches the web-based Agent DevTools — a chat UI plus a live
// view of every iteration, tool call, and streamed token. Set NO_DEVTOOLS=1
// (or DEVTOOLS=0) to fall back to a stdin/stdout REPL instead. The DEVTOOLS
// env var can also pin a specific port, e.g. DEVTOOLS=4050.
//
// BeforeInvoke and AfterInvoke are honoured in both modes, so wiring such as
// tracing exporters or token logging works regardless of which UI is active.
// ClearFunc is exposed as a Clear button in the web UI and as a "clear"
// command in the REPL.
func Chat(c *agent.Context, a *agent.Agent, opts ...ChatOptions) {
	var o ChatOptions
	if len(opts) > 0 {
		o = opts[0]
	}

	if port, on := devtoolsEnvSetting(); on {
		runChatDevTools(c, a, o, port)
		return
	}

	runChatREPL(c, a, o)
}

// runChatREPL is the original stdin/stdout chat loop, retained as the opt-out
// fallback for environments where launching a browser UI is not desirable
// (CI, headless servers, scripted demos).
func runChatREPL(c *agent.Context, a *agent.Agent, o ChatOptions) {
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
			if err := o.ClearFunc(c); err != nil {
				fmt.Printf("Error clearing: %v\n", err)
			} else {
				fmt.Println("Conversation cleared.")
			}
			continue
		}

		invokeCtx := cloneAgentContextOrBackground(c)
		if o.BeforeInvoke != nil {
			if updated := o.BeforeInvoke(invokeCtx, input); updated != nil {
				invokeCtx = updated
			}
		}

		var (
			result    agent.Result
			invokeErr error
		)
		for ev, streamErr := range a.Stream(invokeCtx, input) {
			if ev.Type == agent.EventText && ev.Text != nil {
				fmt.Print(ev.Text.Content)
			}
			if ev.Type == agent.EventEnd && ev.Result != nil {
				result = *ev.Result
			}
			if streamErr != nil {
				invokeErr = streamErr
			}
		}
		fmt.Println()

		if o.AfterInvoke != nil {
			o.AfterInvoke(invokeCtx, result, invokeErr)
		}

		if invokeErr != nil {
			fmt.Printf("Error: %v\n", invokeErr)
			continue
		}
	}
}

// ClearConversation returns a ClearFunc that deletes a conversation. Deletion
// is administrative behavior exposed by ConversationManager; normal agent
// persistence uses ConversationStore snapshots and revision-checked saves.
func ClearConversation(m agent.ConversationManager, conversationID string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return m.Delete(ctx, conversationID)
	}
}

// devtoolsEnvSetting reports whether the DevTools UI should be used and on
// which port. DevTools is the default; the user opts out via either
// NO_DEVTOOLS=1 or DEVTOOLS=0/false/no/off.
//
// Recognized DEVTOOLS values:
//   - unset, "1", "true", "yes", "on": DevTools on the default port (0 → NewAgentDevTools fallback)
//   - "0", "false", "no", "off": DevTools off (stdin/stdout REPL)
//   - any positive integer: DevTools on that port
//
// NO_DEVTOOLS takes precedence — if it's set to a truthy value, DevTools is
// always disabled regardless of DEVTOOLS.
func devtoolsEnvSetting() (int, bool) {
	if isTruthy(os.Getenv("NO_DEVTOOLS")) {
		return 0, false
	}
	v := strings.TrimSpace(os.Getenv("DEVTOOLS"))
	if v == "" {
		return 0, true
	}
	switch strings.ToLower(v) {
	case "0", "false", "no", "off":
		return 0, false
	case "1", "true", "yes", "on":
		return 0, true
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
		return n, true
	}
	return 0, true
}

// isTruthy reports whether s looks like a "yes" environment value.
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// runChatDevTools starts the AgentDevTools server, wiring BeforeInvoke /
// AfterInvoke from ChatOptions. It blocks until the server exits.
func runChatDevTools(base *agent.Context, a *agent.Agent, o ChatOptions, port int) {
	dt := NewAgentDevTools(AgentDevToolsConfig{
		Port:  port,
		Agent: a,
		NewContext: func(input string) *agent.Context {
			c := cloneAgentContextOrBackground(base)
			if o.BeforeInvoke != nil {
				if updated := o.BeforeInvoke(c, input); updated != nil {
					c = updated
				}
			}
			return c
		},
		OnTurnEnd: o.AfterInvoke,
		OnClear:   o.ClearFunc,
	})
	if err := dt.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// cloneAgentContextOrBackground returns a Clone of c when non-nil, falling
// back to agent.Background(). Cloning isolates KV state so per-turn writes
// don't bleed between invocations.
func cloneAgentContextOrBackground(c *agent.Context) *agent.Context {
	if c == nil {
		return agent.Background()
	}
	return c.Clone()
}
