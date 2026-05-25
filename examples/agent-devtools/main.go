// Example: agent-devtools — open a web UI to chat with an agent and watch
// every iteration, tool call, and streamed token in real time. The DevTools
// server consumes Agent.InvokeEventStream so the UI gets a faithful trace of
// the agent loop.
//
// Run:
//
//	go run ./agent-devtools
//
// Then open http://localhost:4041 (the example utility opens the browser
// automatically). Type a question, then watch the right-hand timeline and
// the inline iteration cards as the agent calls tools and streams its reply.
//
// Try a question that needs tool calls, e.g.:
//
//	"What's the weather in Berlin and what time is it there?"
package main

import (
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	provider := bedrock.Must(bedrock.GlobalClaudeSonnet4_6())

	// Use a shared in-memory conversation so the UI can carry context across
	// turns without having to reconfigure the agent for each message.
	store := conversation.NewInMemory()
	const conversationID = "agent-devtools-session"

	a, err := agent.Default(
		provider,
		prompt.Text("You are a concise, helpful assistant. Use the available tools when they help."),
		[]tool.Tool{
			utils.WeatherTool(),
			utils.TimeTool(),
			utils.CalculateTool(),
		},
		agent.WithConversation(store, conversationID),
		agent.WithMaxIterations(8),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	dt := utils.NewAgentDevTools(utils.AgentDevToolsConfig{
		Port:      4041,
		Agent:     a,
		AgentName: "weather-bot",
		NewContext: func() *agent.Context {
			return agent.Background().WithConversationID(conversationID)
		},
	})

	if err := dt.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
