// Run:
//
//	go run ./background-deploy
//
// This example demonstrates a background tool that simulates a service deployment
// taking 20-30 seconds. The agent returns an immediate ack ("Deployment started"),
// the handler runs in the background, and when it completes the agent automatically
// re-enters the conversation to report the result via the notification callback.

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
	deployTool := tool.NewBackground(
		"deploy_service",
		"Deploy a service to a specific version and region. This takes 20-30 seconds.",
		"Deployment initiated — I'll notify you when it completes.",
		func(ctx context.Context, in DeployInput) (string, error) {
			log.Printf("[deploy] Starting deployment: %s@%s → %s", in.Service, in.Version, in.Region)

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
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}

			log.Printf("[deploy] Deployment complete: %s@%s in %s", in.Service, in.Version, in.Region)
			return fmt.Sprintf("Successfully deployed %s@%s to %s. All health checks passing. 0 errors in the last 60s.",
				in.Service, in.Version, in.Region), nil
		},
	)

	a, err := agent.New(
		provider,
		`You are a DevOps assistant that helps deploy services.
When the user asks to deploy something, use the deploy_service tool.
Be concise and professional.`,
		agent.WithTools(deployTool),
		agent.WithConversationStore(store),
		agent.WithName("deploy-assistant"),
		agent.WithBackgroundNotify(func(conversationID, agentMessage string) {
			fmt.Printf("\n📬 [Background notification on %s]:\n%s\n", conversationID, agentMessage)
		}),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("👤 User: Deploy the payments service to v2.4.1 in us-east-1")
	fmt.Println()

	ctx := agent.Background().WithConversationID("deploy-session")
	result, err := a.Invoke(ctx, "Deploy the payments service to v2.4.1 in us-east-1")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("🤖 Agent (immediate): %s\n", result.Text)
	fmt.Println()
	fmt.Println("⏳ Deployment running in background... (the HTTP request would have returned by now)")
	fmt.Println()

	// In a real app, the HTTP handler would return here and the user would
	// receive the notification via SSE/websocket/push. This demo waits for all
	// background work and its re-entry turn to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n✅ All background work complete.")
}
