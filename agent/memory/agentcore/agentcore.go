// Package agentcore provides an AWS Bedrock AgentCore Memory backend for the
// [memory.Memory] interface. It enables agents to persist and retrieve
// facts using AgentCore's managed memory service with built-in semantic
// search.
//
// # Setup
//
// Create an AgentCore Memory resource in your AWS account, then pass the
// bedrockagentcore client and memory ID to [New]:
//
//	cfg, _ := config.LoadDefaultConfig(ctx)
//	client := bedrockagentcore.NewFromConfig(cfg)
//	store, err := agentcore.New(client, "my-memory-id")
//
// # Store Modes
//
// The backend supports two storage modes selected via [WithStoreMode]:
//
//   - [CreateEventMode] (default) — sends facts as conversational events.
//     AgentCore's long-term memory strategies automatically extract and store
//     insights.
//   - [BatchCreateMode] — writes facts directly as memory records, bypassing
//     automatic extraction.
//
// # Namespace Template
//
// Memory records are scoped by a namespace derived from the actor ID. The
// default template is "/facts/{{.ActorID}}/". Use [WithNamespaceTemplate] to
// customise the namespace hierarchy for multi-tenant applications.
package agentcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/google/uuid"
)

// AgentCoreAPI defines the subset of the bedrockagentcore.Client used by the
// Store. Accepting an interface rather than the concrete client enables
// unit testing with a mock.
type AgentCoreAPI interface {
	CreateEvent(ctx context.Context, params *bedrockagentcore.CreateEventInput,
		optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error)
	BatchCreateMemoryRecords(ctx context.Context, params *bedrockagentcore.BatchCreateMemoryRecordsInput,
		optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error)
	RetrieveMemoryRecords(ctx context.Context, params *bedrockagentcore.RetrieveMemoryRecordsInput,
		optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error)
}

// Store implements memory.Memory using AWS Bedrock AgentCore Memory.
// Documented in docs/memory.md
type Store struct {
	client      AgentCoreAPI
	memoryID    string
	nsTmpl      *template.Template
	sessionIDFn func() string
	mode        StoreMode
}

// Compile-time assertion that Store satisfies the Memory interface.
var _ memory.Memory = (*Store)(nil)

// namespaceData is the data struct passed to the namespace template.
type namespaceData struct {
	ActorID string
}

// New creates a new AgentCore memory Store.
//
// The client must be a non-nil AgentCore API client (typically created via
// bedrockagentcore.NewFromConfig). The memoryID identifies the AgentCore
// Memory resource to use.
//
// Options:
//   - [WithStoreMode]: select CreateEvent or BatchCreateMemoryRecords (default: CreateEvent)
//   - [WithNamespaceTemplate]: Go text/template for namespace generation (default: "/facts/{{.ActorID}}/")
//   - [WithSessionIDFunc]: function for generating session IDs (default: UUID)
//
// Returns an error if client is nil, memoryID is empty, or the namespace
// template fails to parse.
func New(client AgentCoreAPI, memoryID string, opts ...Option) (*Store, error) {
	if client == nil {
		return nil, errors.New("agentcore memory: client is required")
	}
	if memoryID == "" {
		return nil, errors.New("agentcore memory: memory ID is required")
	}

	cfg := config{
		nsTmplStr:   "/facts/{{.ActorID}}/",
		sessionIDFn: uuid.NewString,
		mode:        CreateEventMode,
	}
	for _, o := range opts {
		o(&cfg)
	}

	tmpl, err := template.New("namespace").Parse(cfg.nsTmplStr)
	if err != nil {
		return nil, fmt.Errorf("agentcore memory: parse namespace template: %w", err)
	}

	return &Store{
		client:      client,
		memoryID:    memoryID,
		nsTmpl:      tmpl,
		sessionIDFn: cfg.sessionIDFn,
		mode:        cfg.mode,
	}, nil
}

// renderNamespace executes the namespace template with the given identifier
// as the ActorID.
func (s *Store) renderNamespace(identifier string) (string, error) {
	var buf bytes.Buffer
	if err := s.nsTmpl.Execute(&buf, namespaceData{ActorID: identifier}); err != nil {
		return "", fmt.Errorf("agentcore memory: render namespace: %w", err)
	}
	return buf.String(), nil
}

// Remember stores a fact for the given identifier. Metadata is optional.
// Returns an error if identifier or fact is empty.
func (s *Store) Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	if identifier == "" {
		return errors.New("agentcore memory: identifier must not be empty")
	}
	if fact == "" {
		return errors.New("agentcore memory: fact must not be empty")
	}

	switch s.mode {
	case CreateEventMode:
		return s.rememberCreateEvent(ctx, identifier, fact, metadata)
	case BatchCreateMode:
		return s.rememberBatchCreate(ctx, identifier, fact, metadata)
	default:
		return fmt.Errorf("agentcore memory: unknown store mode: %d", s.mode)
	}
}

// rememberCreateEvent stores a fact by sending it as a conversational event
// to AgentCore. Long-term memory strategies automatically extract insights.
func (s *Store) rememberCreateEvent(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	sessionID := s.sessionIDFn()

	input := &bedrockagentcore.CreateEventInput{
		MemoryId:       &s.memoryID,
		ActorId:        &identifier,
		SessionId:      &sessionID,
		EventTimestamp: aws.Time(time.Now()),
		Payload: []types.PayloadType{
			&types.PayloadTypeMemberConversational{
				Value: types.Conversational{
					Content: &types.ContentMemberText{
						Value: fact,
					},
					Role: types.RoleUser,
				},
			},
		},
	}

	if len(metadata) > 0 {
		md := make(map[string]types.MetadataValue, len(metadata))
		for k, v := range metadata {
			md[k] = &types.MetadataValueMemberStringValue{Value: v}
		}
		input.Metadata = md
	}

	if _, err := s.client.CreateEvent(ctx, input); err != nil {
		return fmt.Errorf("agentcore memory: create event: %w", err)
	}
	return nil
}

// rememberBatchCreate stores a fact directly as a memory record, bypassing
// automatic extraction.
func (s *Store) rememberBatchCreate(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	namespace, err := s.renderNamespace(identifier)
	if err != nil {
		return err
	}

	record := types.MemoryRecordCreateInput{
		Content: &types.MemoryContentMemberText{
			Value: fact,
		},
		Namespaces:        []string{namespace},
		Timestamp:         aws.Time(time.Now()),
		RequestIdentifier: aws.String(s.sessionIDFn()),
	}

	input := &bedrockagentcore.BatchCreateMemoryRecordsInput{
		MemoryId: &s.memoryID,
		Records:  []types.MemoryRecordCreateInput{record},
	}

	if _, err := s.client.BatchCreateMemoryRecords(ctx, input); err != nil {
		return fmt.Errorf("agentcore memory: batch create: %w", err)
	}
	return nil
}

// Recall retrieves the top entries for the given identifier by semantic
// similarity to the query. Returns at most limit results, ordered by
// descending score.
func (s *Store) Recall(ctx context.Context, identifier, query string, limit int) ([]memory.Entry, error) {
	if identifier == "" {
		return nil, errors.New("agentcore memory: identifier must not be empty")
	}
	if limit < 1 {
		return nil, errors.New("agentcore memory: limit must be at least 1")
	}

	namespace, err := s.renderNamespace(identifier)
	if err != nil {
		return nil, err
	}

	input := &bedrockagentcore.RetrieveMemoryRecordsInput{
		MemoryId:   &s.memoryID,
		Namespace:  &namespace,
		MaxResults: aws.Int32(int32(limit)),
		SearchCriteria: &types.SearchCriteria{
			SearchQuery: &query,
		},
	}

	out, err := s.client.RetrieveMemoryRecords(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("agentcore memory: retrieve: %w", err)
	}

	entries := make([]memory.Entry, 0, len(out.MemoryRecordSummaries))
	for _, summary := range out.MemoryRecordSummaries {
		entry := memory.Entry{
			Score:     aws.ToFloat64(summary.Score),
			CreatedAt: aws.ToTime(summary.CreatedAt),
			Metadata:  convertMetadata(summary.Metadata),
		}
		if textContent, ok := summary.Content.(*types.MemoryContentMemberText); ok {
			entry.Fact = textContent.Value
		}
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return []memory.Entry{}, nil
	}

	return entries, nil
}

// convertMetadata converts AgentCore MetadataValue map to a plain string map.
func convertMetadata(md map[string]types.MetadataValue) map[string]string {
	if len(md) == 0 {
		return nil
	}
	result := make(map[string]string, len(md))
	for k, v := range md {
		if sv, ok := v.(*types.MetadataValueMemberStringValue); ok {
			result[k] = sv.Value
		}
	}
	return result
}
