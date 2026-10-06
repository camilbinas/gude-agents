# Getting started

## Install

```bash
go get github.com/camilbinas/gude-agents/agent
```

Provider and backend packages are separate Go modules. Add only the packages used by your application.

## Create and invoke an agent

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/conversation"
    "github.com/camilbinas/gude-agents/agent/provider/openai"
    "github.com/camilbinas/gude-agents/agent/tool"
)

type WeatherInput struct {
    City string `json:"city" description:"City to look up" required:"true"`
}

func main() {
    weather := tool.New("weather", "Get current weather", func(ctx context.Context, in WeatherInput) (string, error) {
        return "18 C in " + in.City, nil
    })

    prov, err := openai.New("gpt-4o-mini")
    if err != nil {
        log.Fatal(err)
    }
    a, err := agent.New(prov, "Answer clearly and use tools when useful.",
        agent.WithTools(weather),
        agent.WithConversationStore(conversation.NewInMemory()),
    )
    if err != nil {
        log.Fatal(err)
    }

    ctx := agent.NewContext(context.Background()).
        WithConversationID("demo-thread").
        WithIdentity("user-123")

    result, err := a.Invoke(ctx, "What is the weather in Lisbon?")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.Text)
    fmt.Println("tokens:", result.Usage.Total())

    if err := a.Shutdown(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

`Agent` configuration is fixed after `New`, except a supplied dynamic `tool.Registry`. Configure each call by chaining mutable `Context.With…` methods before invocation.

Because this Agent has a conversation store, every call needs `WithConversationID`; omitting it returns `agent.ErrConversationIDRequired` before the model runs. For one-off stateless calls, construct the Agent without `WithConversationStore`.

## Stream application events

```go
for event, err := range a.Stream(ctx.WithDetailedEvents(), "Explain the forecast") {
    if err != nil {
        log.Fatal(err)
    }
    switch event.Type {
    case agent.EventText:
        fmt.Print(event.Text.Content)
    case agent.EventInterrupt:
        fmt.Printf("\npaused execution: %s\n", event.Interrupt.ExecutionID)
    case agent.EventEnd:
        result = *event.Result
    }
}
```

For live text only:

```go
for chunk, err := range a.TextStream(ctx, "Explain the forecast") {
    if err != nil {
        log.Fatal(err)
    }
    fmt.Print(chunk)
}
```

Breaking out of either iterator cancels the invocation. Consume through `EventEnd` when the completed turn must be persisted.

## Continue an interrupt

An interrupt is a successful paused result, not an error:

```go
result, err := a.Invoke(ctx, "Delete order 42")
if err != nil {
    log.Fatal(err)
}
if result.StopReason == agent.StopInterrupt {
    result, err = a.Resume(ctx, result.Interrupt, agent.Approve())
}
```

Use `Deny(reason)` or `Decide(map[callID]tool.Decision)` for approval interrupts and `Respond(text)` for human-input interrupts. See [Interrupts](interrupts.md).

## Next

Read [Agent API](agent-api.md), [Invocation context](invocation-context.md), [Tools](tools.md), and [Conversation persistence](conversation.md).
