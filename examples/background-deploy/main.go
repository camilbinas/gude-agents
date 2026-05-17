// Run:
//
//	go run ./background-deploy
//
// This example demonstrates a Background_Tool that simulates a service deployment
// taking 20-30 seconds. The agent returns an immediate ack ("Deployment started"),
// the handler runs in the background, and when it completes the agent automatically
// re-enters the conversation to report the result via the Notify_Callback.

package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

type DeployInput struct {
	Service string `json:"service" description:"Name of the service to deploy" required:"true"`
	Version string `json:"version" description:"Version tag to deploy"        required:"true"`
	Region  string `json:"region"  description:"Target AWS region"            enum:"us-east-1,eu-west-1,ap-southeast-1"`
}

func main() {
	provider := bedrock.Must(bedrock.Standard())
	store := conversation.NewInMemory()

	// The deploy tool simulates a 20-30s deployment pipeline.
	deployTool := tool.NewBackground("deploy_service",
		"Deploy a service to a specific version and region. This takes 20-30 seconds.",
		"Deployment initiated — I'll notify you when it completes.",
		func(ctx context.Context, in DeployInput) (string, error) {
			log.Printf("[deploy] Starting deployment: %s@%s → %s", in.Service, in.Version, in.Region)

			// Simulate deployment steps.
			steps := []string{
				"pulling container image",
				"running pre-deploy checks",
				"rolling out new pods",
				"waiting for health checks",
				"switching traffic",
			}

			for i, step := range steps {
				delay := time.Duration(4+rand.Intn(3)) * time.Second
				log.Printf("[deploy] Step %d/%d: %s...", i+1, len(steps), step)
				time.Sleep(delay)
			}

			log.Printf("[deploy] Deployment complete: %s@%s in %s", in.Service, in.Version, in.Region)
			return fmt.Sprintf("Successfully deployed %s@%s to %s. All health checks passing. 0 errors in the last 60s.",
				in.Service, in.Version, in.Region), nil
		},
	)

	a, err := agent.New(
		provider,
		prompt.Text(`You are a DevOps assistant that helps deploy services.
When the user asks to deploy something, use the deploy_service tool.
Be concise and professional.`),
		[]tool.Tool{deployTool},
		agent.WithConversation(store, "deploy-session"),
		agent.WithName("deploy-assistant"),
		agent.WithBackgroundNotify(func(conversationID, agentMessage string) {
			fmt.Printf("\n📬 [Background notification on %s]:\n%s\n", conversationID, agentMessage)
		}),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	// Ask the agent to deploy — it returns immediately with the ack.
	fmt.Println("👤 User: Deploy the payments service to v2.4.1 in us-east-1")
	fmt.Println()

	result, err := a.Invoke(agent.Background(), "Deploy the payments service to v2.4.1 in us-east-1")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("🤖 Agent (immediate): %s\n", result)
	fmt.Println()
	fmt.Println("⏳ Deployment running in background... (the HTTP request would have returned by now)")
	fmt.Println()

	// In a real app, the HTTP handler would return here and the user would
	// receive the notification via SSE/websocket/push when the deployment finishes.
	// For this demo, we just wait for Close() which blocks until all background
	// work completes.
	a.Close()

	fmt.Println("\n✅ All background work complete.")
}
