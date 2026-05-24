package agentcore

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestWithConversationAWSConfig(t *testing.T) {
	awsCfg := aws.Config{Region: "eu-west-1"}
	cfg := conversationConfig{}

	opt := WithConversationAWSConfig(awsCfg)
	if err := opt(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.awsCfg == nil {
		t.Fatal("expected awsCfg to be set")
	}
	if cfg.awsCfg.Region != "eu-west-1" {
		t.Errorf("expected region eu-west-1, got %q", cfg.awsCfg.Region)
	}
}

func TestWithMemoryID(t *testing.T) {
	cfg := conversationConfig{}

	opt := WithMemoryID("mem-12345")
	if err := opt(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.memoryID != "mem-12345" {
		t.Errorf("expected memoryID 'mem-12345', got %q", cfg.memoryID)
	}
}

func TestWithActorID(t *testing.T) {
	cfg := conversationConfig{}

	opt := WithActorID("actor-abc")
	if err := opt(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.actorID != "actor-abc" {
		t.Errorf("expected actorID 'actor-abc', got %q", cfg.actorID)
	}
}

func TestConversationOptionsComposition(t *testing.T) {
	awsCfg := aws.Config{Region: "ap-southeast-1"}
	cfg := conversationConfig{}

	opts := []ConversationOption{
		WithConversationAWSConfig(awsCfg),
		WithMemoryID("mem-xyz"),
		WithActorID("user-1"),
	}

	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if cfg.awsCfg == nil || cfg.awsCfg.Region != "ap-southeast-1" {
		t.Errorf("expected region ap-southeast-1, got %v", cfg.awsCfg)
	}
	if cfg.memoryID != "mem-xyz" {
		t.Errorf("expected memoryID 'mem-xyz', got %q", cfg.memoryID)
	}
	if cfg.actorID != "user-1" {
		t.Errorf("expected actorID 'user-1', got %q", cfg.actorID)
	}
}
