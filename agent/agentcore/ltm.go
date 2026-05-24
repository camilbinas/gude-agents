package agentcore

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
)

// LTMStore wraps AgentCore's server-side long-term memory API.
// It provides string-based semantic storage and retrieval without
// requiring a local embedder (AgentCore handles embeddings server-side).
//
// LTMStore is safe for concurrent use — all fields are immutable after
// construction and the underlying AWS SDK client handles concurrency.
type LTMStore struct {
	client    agentCoreClient
	namespace string
}

// LTMResult represents a single search result from the LTM API.
type LTMResult struct {
	ID       string            `json:"id"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Score    float64           `json:"score"`
}

// NewLTMStore creates a long-term memory store backed by AgentCore's
// server-side memory API.
//
// A namespace is required to scope entries. If no AWS configuration is
// provided via WithLTMAWSConfig, the default configuration is loaded
// from the environment.
func NewLTMStore(opts ...LTMOption) (*LTMStore, error) {
	var cfg ltmConfig
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	if cfg.namespace == "" {
		return nil, ErrLTMNamespaceRequired
	}

	// If a client was injected (test path), use it directly.
	if cfg.client != nil {
		return &LTMStore{client: cfg.client, namespace: cfg.namespace}, nil
	}

	// Otherwise, load AWS config and create the SDK client.
	if cfg.awsCfg == nil {
		awsCfg, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("agentcore ltm: loading AWS config: %w", err)
		}
		cfg.awsCfg = &awsCfg
	}

	client := bedrockagentcore.NewFromConfig(*cfg.awsCfg)
	return &LTMStore{client: client, namespace: cfg.namespace}, nil
}

// Store persists a text entry with optional metadata and returns the
// server-assigned entry ID.
func (s *LTMStore) Store(ctx context.Context, content string, metadata map[string]string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("agentcore ltm: nil context")
	}
	if content == "" {
		return "", ErrLTMContentRequired
	}

	// Build metadata map for the SDK type.
	var sdkMetadata map[string]types.MemoryRecordMetadataValue
	if metadata != nil {
		sdkMetadata = make(map[string]types.MemoryRecordMetadataValue, len(metadata))
		for k, v := range metadata {
			sdkMetadata[k] = &types.MemoryRecordMetadataValueMemberStringValue{Value: v}
		}
	}

	now := time.Now()
	reqID := fmt.Sprintf("%s-%d", s.namespace, now.UnixNano())

	input := &bedrockagentcore.BatchCreateMemoryRecordsInput{
		MemoryId: aws.String(s.namespace),
		Records: []types.MemoryRecordCreateInput{
			{
				Content:           &types.MemoryContentMemberText{Value: content},
				Namespaces:        []string{s.namespace},
				RequestIdentifier: aws.String(reqID),
				Timestamp:         &now,
				Metadata:          sdkMetadata,
			},
		},
	}

	output, err := s.client.BatchCreateMemoryRecords(ctx, input)
	if err != nil {
		return "", fmt.Errorf("agentcore ltm: %w", err)
	}

	// Check for failed records.
	if len(output.FailedRecords) > 0 {
		msg := "record creation failed"
		if output.FailedRecords[0].ErrorMessage != nil {
			msg = *output.FailedRecords[0].ErrorMessage
		}
		return "", fmt.Errorf("agentcore ltm: %s", msg)
	}

	// Return the server-assigned ID from the first successful record.
	if len(output.SuccessfulRecords) > 0 && output.SuccessfulRecords[0].MemoryRecordId != nil {
		return *output.SuccessfulRecords[0].MemoryRecordId, nil
	}

	return "", fmt.Errorf("agentcore ltm: no record ID returned")
}

// Search performs semantic search and returns up to topK results ordered
// by relevance (descending score).
func (s *LTMStore) Search(ctx context.Context, query string, topK int) ([]LTMResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("agentcore ltm: nil context")
	}
	if query == "" {
		return nil, ErrLTMQueryRequired
	}
	if topK < 1 {
		return nil, fmt.Errorf("agentcore ltm: topK must be at least 1")
	}

	topK32 := int32(topK)
	input := &bedrockagentcore.RetrieveMemoryRecordsInput{
		MemoryId:  aws.String(s.namespace),
		Namespace: aws.String(s.namespace),
		SearchCriteria: &types.SearchCriteria{
			SearchQuery: aws.String(query),
			TopK:        &topK32,
		},
		MaxResults: &topK32,
	}

	output, err := s.client.RetrieveMemoryRecords(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("agentcore ltm: %w", err)
	}

	results := make([]LTMResult, 0, len(output.MemoryRecordSummaries))
	for _, rec := range output.MemoryRecordSummaries {
		result := LTMResult{}

		if rec.MemoryRecordId != nil {
			result.ID = *rec.MemoryRecordId
		}

		// Extract text content from the MemoryContent interface.
		if textContent, ok := rec.Content.(*types.MemoryContentMemberText); ok {
			result.Content = textContent.Value
		}

		// Convert metadata values (only string values are mapped).
		if rec.Metadata != nil {
			result.Metadata = make(map[string]string, len(rec.Metadata))
			for k, v := range rec.Metadata {
				if sv, ok := v.(*types.MemoryRecordMetadataValueMemberStringValue); ok {
					result.Metadata[k] = sv.Value
				}
			}
		}

		if rec.Score != nil {
			result.Score = *rec.Score
		}

		results = append(results, result)
	}

	return results, nil
}

// Delete removes a single entry by ID. Returns nil if the ID does not exist
// (idempotent deletion).
func (s *LTMStore) Delete(ctx context.Context, id string) error {
	if ctx == nil {
		return fmt.Errorf("agentcore ltm: nil context")
	}
	if id == "" {
		return ErrLTMIDRequired
	}

	input := &bedrockagentcore.DeleteMemoryRecordInput{
		MemoryId:       aws.String(s.namespace),
		MemoryRecordId: aws.String(id),
	}

	_, err := s.client.DeleteMemoryRecord(ctx, input)
	if err != nil {
		// Treat 404 as success (idempotent deletion).
		if is404(err) {
			return nil
		}
		return fmt.Errorf("agentcore ltm: %w", err)
	}

	return nil
}
