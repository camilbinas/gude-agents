package agentcore

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
)

// Compile-time check: *bedrockagentcore.Client satisfies agentCoreClient.
var _ agentCoreClient = (*bedrockagentcore.Client)(nil)

// agentCoreClient is the interface for AgentCore data-plane operations.
// The concrete *bedrockagentcore.Client satisfies this interface.
type agentCoreClient interface {
	// Runtime operations
	InvokeAgentRuntime(ctx context.Context, params *bedrockagentcore.InvokeAgentRuntimeInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error)

	// Memory / Session operations (for Conversation store)
	CreateEvent(ctx context.Context, params *bedrockagentcore.CreateEventInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error)
	ListEvents(ctx context.Context, params *bedrockagentcore.ListEventsInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error)
	ListSessions(ctx context.Context, params *bedrockagentcore.ListSessionsInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error)
	StopRuntimeSession(ctx context.Context, params *bedrockagentcore.StopRuntimeSessionInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error)

	// Browser operations
	StartBrowserSession(ctx context.Context, params *bedrockagentcore.StartBrowserSessionInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error)
	InvokeBrowser(ctx context.Context, params *bedrockagentcore.InvokeBrowserInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error)
	StopBrowserSession(ctx context.Context, params *bedrockagentcore.StopBrowserSessionInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error)

	// Code interpreter operations
	StartCodeInterpreterSession(ctx context.Context, params *bedrockagentcore.StartCodeInterpreterSessionInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error)
	InvokeCodeInterpreter(ctx context.Context, params *bedrockagentcore.InvokeCodeInterpreterInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error)
	StopCodeInterpreterSession(ctx context.Context, params *bedrockagentcore.StopCodeInterpreterSessionInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error)

	// Long-Term Memory (LTM) operations
	BatchCreateMemoryRecords(ctx context.Context, params *bedrockagentcore.BatchCreateMemoryRecordsInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error)
	RetrieveMemoryRecords(ctx context.Context, params *bedrockagentcore.RetrieveMemoryRecordsInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error)
	DeleteMemoryRecord(ctx context.Context, params *bedrockagentcore.DeleteMemoryRecordInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error)
}
