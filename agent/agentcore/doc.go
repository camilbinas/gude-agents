// Package agentcore integrates gude-agents with AWS Bedrock AgentCore.
//
// It provides three integration surfaces:
//
//   - Runtime: A long-running worker adapter that registers with AgentCore,
//     heartbeats, polls for incoming events, routes them through an *agent.Agent,
//     and submits responses back. This is the "deploy to AgentCore" entry point.
//
//   - Conversation: An agent.Conversation implementation backed by AgentCore's
//     Memory service (sessions and events), eliminating the need for a separate
//     DynamoDB table or other external store.
//
//   - Built-in Tools: tool.Tool values wrapping AgentCore's managed browser and
//     code interpreter services.
//
// The package lives in its own Go module to keep AgentCore SDK dependencies
// isolated from the core agent library. Consumers who do not deploy to AgentCore
// will not pull in these dependencies.
//
// # Usage
//
// Create a Runtime and call Run to deploy an existing agent to AgentCore:
//
//	rt, err := agentcore.NewRuntime(myAgent,
//	    agentcore.WithAgentName("my-agent"),
//	    agentcore.WithAutoConversation(),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if err := rt.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// Use the AgentCore conversation store independently:
//
//	conv, err := agentcore.NewConversation(
//	    agentcore.WithMemoryID("my-memory"),
//	)
//
// Add AgentCore's managed tools to an agent:
//
//	browser := agentcore.NewBrowserTool()
//	codeInterp := agentcore.NewCodeInterpreterTool()
package agentcore
