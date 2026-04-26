// Package blob provides an S3-compatible memory driver for the gude-agents framework.
// It stores conversation history as JSON objects in any S3-compatible object store.
//
// Tested compatible providers: AWS S3, Cloudflare R2, MinIO (use WithPathStyle),
// DigitalOcean Spaces, Backblaze B2.
// Other providers with S3-compatible APIs may work but are not guaranteed.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

// s3Client is the interface for S3 operations used by S3Memory.
// The concrete *s3.Client satisfies this interface.
type s3Client interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// Compile-time interface checks.
var _ agent.Conversation = (*S3Conversation)(nil)
var _ conversation.ConversationManager = (*S3Conversation)(nil)

// S3Conversation implements agent.Conversation and conversation.ConversationManager using an
// S3-compatible object store.
type S3Conversation struct {
	client    s3Client
	bucket    string
	keyPrefix string
}

// NewS3Memory creates a new S3Memory. No network calls are made at
// construction time; connectivity errors surface on the first Save/Load call.
//
// Returns an error if bucket is empty.
func New(cfg aws.Config, bucket string, opts ...S3ConversationOption) (*S3Conversation, error) {
	if bucket == "" {
		return nil, fmt.Errorf("s3 conversation: bucket name is required")
	}

	c := &s3ConversationConfig{
		keyPrefix: "",
	}
	for _, o := range opts {
		o(c)
	}

	s3Opts := []func(*s3.Options){}
	if c.endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(c.endpoint)
		})
	}
	if c.pathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		for _, opt := range s3Opts {
			opt(o)
		}
	})

	return &S3Conversation{
		client:    client,
		bucket:    bucket,
		keyPrefix: c.keyPrefix,
	}, nil
}

// Save persists messages for the given conversation ID as a JSON object in S3.
func (m *S3Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return fmt.Errorf("s3 conversation: save: %w", err)
	}

	_, err = m.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(m.bucket),
		Key:         aws.String(m.keyPrefix + conversationID),
		Body:        strings.NewReader(string(data)),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("s3 conversation: save: %w", err)
	}

	return nil
}

// Load retrieves messages for the given conversation ID.
// Returns an empty non-nil slice if the object does not exist.
func (m *S3Conversation) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	out, err := m.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(m.keyPrefix + conversationID),
	})
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return []agent.Message{}, nil
		}
		return nil, fmt.Errorf("s3 conversation: load: %w", err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("s3 conversation: load: %w", err)
	}

	messages, err := conversation.UnmarshalMessages(data)
	if err != nil {
		return nil, fmt.Errorf("s3 conversation: load: %w", err)
	}

	return messages, nil
}

// List returns all conversation IDs whose objects share the configured key prefix.
func (m *S3Conversation) List(ctx context.Context) ([]string, error) {
	var ids []string
	var continuationToken *string

	for {
		out, err := m.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(m.bucket),
			Prefix:            aws.String(m.keyPrefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("s3 conversation: list: %w", err)
		}

		for _, obj := range out.Contents {
			if obj.Key != nil {
				ids = append(ids, strings.TrimPrefix(*obj.Key, m.keyPrefix))
			}
		}

		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	return ids, nil
}

// Delete removes the object for the given conversation ID.
// A not-found response is not treated as an error.
func (m *S3Conversation) Delete(ctx context.Context, conversationID string) error {
	_, err := m.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(m.keyPrefix + conversationID),
	})
	if err != nil {
		return fmt.Errorf("s3 conversation: delete: %w", err)
	}

	return nil
}
