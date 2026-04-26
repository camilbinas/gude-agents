package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/camilbinas/gude-agents/agent"
)

// mockS3Client is an in-memory implementation of the s3Client interface for testing.
type mockS3Client struct {
	objects map[string][]byte

	putErr    error
	getErr    error
	deleteErr error
	listErr   error
}

func newMockS3Client() *mockS3Client {
	return &mockS3Client{
		objects: make(map[string][]byte),
	}
}

func (m *mockS3Client) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.putErr != nil {
		return nil, m.putErr
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	m.objects[aws.ToString(in.Key)] = body
	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3Client) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	key := aws.ToString(in.Key)
	data, ok := m.objects[key]
	if !ok {
		return nil, &s3types.NoSuchKey{Message: aws.String("NoSuchKey")}
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

func (m *mockS3Client) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	delete(m.objects, aws.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func (m *mockS3Client) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	prefix := aws.ToString(in.Prefix)
	var contents []s3types.Object
	for key := range m.objects {
		if strings.HasPrefix(key, prefix) {
			k := key
			contents = append(contents, s3types.Object{Key: &k})
		}
	}
	return &s3.ListObjectsV2Output{
		Contents: contents,
	}, nil
}

// --- Constructor tests ---

// TestNew_EmptyBucket verifies that an empty bucket name returns an error.
// Req 1.4
func TestNew_EmptyBucket(t *testing.T) {
	_, err := New(aws.Config{}, "")
	if err == nil {
		t.Fatal("expected error for empty bucket, got nil")
	}
	if !strings.Contains(err.Error(), "bucket name is required") {
		t.Errorf("expected error to contain %q, got %q", "bucket name is required", err.Error())
	}
}

// TestNew_LazyConstruction verifies that valid args return a non-nil S3Conversation
// without making any network calls.
// Req 1.3
func TestNew_LazyConstruction(t *testing.T) {
	m, err := New(aws.Config{}, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil S3Conversation")
	}
}

// TestS3Conversation_DefaultKeyPrefix verifies that keyPrefix defaults to "".
// Req 2.1
func TestS3Conversation_DefaultKeyPrefix(t *testing.T) {
	m, err := New(aws.Config{}, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.keyPrefix != "" {
		t.Errorf("expected keyPrefix %q, got %q", "", m.keyPrefix)
	}
}

// TestS3Conversation_WithEndpoint verifies that construction succeeds when a custom endpoint is provided.
// Req 2.4
func TestS3Conversation_WithEndpoint(t *testing.T) {
	m, err := New(aws.Config{}, "my-bucket", WithEndpoint("http://localhost:9000"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil S3Conversation")
	}
}

// TestS3Conversation_WithPathStyle verifies that construction succeeds when path-style is enabled.
// Req 2.5
func TestS3Conversation_WithPathStyle(t *testing.T) {
	m, err := New(aws.Config{}, "my-bucket", WithPathStyle(true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil S3Conversation")
	}
}

// --- Error wrapping tests ---

// TestS3Conversation_Save_ErrorWrapping verifies that a PutObject error is wrapped with "s3 conversation: save".
// Req 4.3
func TestS3Conversation_Save_ErrorWrapping(t *testing.T) {
	mock := newMockS3Client()
	mock.putErr = errors.New("s3 unavailable")

	m := &S3Conversation{
		client:    mock,
		bucket:    "test-bucket",
		keyPrefix: "",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "s3 conversation: save") {
		t.Errorf("expected error to contain %q, got %q", "s3 conversation: save", err.Error())
	}
}

// TestS3Conversation_Load_ErrorWrapping verifies that a non-404 GetObject error is wrapped with "s3 conversation: load".
// Req 5.3
func TestS3Conversation_Load_ErrorWrapping(t *testing.T) {
	mock := newMockS3Client()
	mock.getErr = errors.New("s3 internal error")

	m := &S3Conversation{
		client:    mock,
		bucket:    "test-bucket",
		keyPrefix: "",
	}

	_, err := m.Load(context.Background(), "conv1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "s3 conversation: load") {
		t.Errorf("expected error to contain %q, got %q", "s3 conversation: load", err.Error())
	}
}

// TestS3Conversation_Delete_ErrorWrapping verifies that a DeleteObject error is wrapped with "s3 conversation: delete".
// Req 6.4
func TestS3Conversation_Delete_ErrorWrapping(t *testing.T) {
	mock := newMockS3Client()
	mock.deleteErr = errors.New("s3 delete error")

	m := &S3Conversation{
		client:    mock,
		bucket:    "test-bucket",
		keyPrefix: "",
	}

	err := m.Delete(context.Background(), "conv1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "s3 conversation: delete") {
		t.Errorf("expected error to contain %q, got %q", "s3 conversation: delete", err.Error())
	}
}

// --- Not-found and edge case tests ---

// TestS3Conversation_Load_NotFound verifies that a NoSuchKey error returns an empty slice and nil error.
// Req 5.2
func TestS3Conversation_Load_NotFound(t *testing.T) {
	mock := newMockS3Client()
	// No objects stored — GetObject will return NoSuchKey.

	m := &S3Conversation{
		client:    mock,
		bucket:    "test-bucket",
		keyPrefix: "",
	}

	msgs, err := m.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if msgs == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty slice, got %d messages", len(msgs))
	}
}

// TestS3Conversation_Save_EmptySlice verifies that saving an empty slice writes "[]" JSON.
// Req 4.2
func TestS3Conversation_Save_EmptySlice(t *testing.T) {
	mock := newMockS3Client()

	m := &S3Conversation{
		client:    mock,
		bucket:    "test-bucket",
		keyPrefix: "",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	key := "conv1"
	data, ok := mock.objects[key]
	if !ok {
		t.Fatalf("expected object at key %q to exist", key)
	}
	if string(data) != "[]" {
		t.Errorf("expected body %q, got %q", "[]", string(data))
	}
}

// TestS3Conversation_Delete_NonExistent verifies that deleting a key that was never saved returns nil error.
// Req 6.3
func TestS3Conversation_Delete_NonExistent(t *testing.T) {
	mock := newMockS3Client()

	m := &S3Conversation{
		client:    mock,
		bucket:    "test-bucket",
		keyPrefix: "",
	}

	err := m.Delete(context.Background(), "never-saved")
	if err != nil {
		t.Errorf("expected nil error for non-existent key, got %v", err)
	}
}
