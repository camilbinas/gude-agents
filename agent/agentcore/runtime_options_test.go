package agentcore

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestDefaultRuntimeConfig(t *testing.T) {
	cfg := defaultRuntimeConfig()

	if cfg.heartbeatInterval != 5*time.Second {
		t.Errorf("expected heartbeatInterval 5s, got %v", cfg.heartbeatInterval)
	}
	if cfg.shutdownTimeout != 30*time.Second {
		t.Errorf("expected shutdownTimeout 30s, got %v", cfg.shutdownTimeout)
	}
	if !cfg.streaming {
		t.Error("expected streaming to be true by default")
	}
	if cfg.maxConcurrency != 10 {
		t.Errorf("expected maxConcurrency 10, got %d", cfg.maxConcurrency)
	}
	if cfg.autoConversation {
		t.Error("expected autoConversation to be false by default")
	}
	if cfg.awsCfg != nil {
		t.Error("expected awsCfg to be nil by default")
	}
	if cfg.agentName != "" {
		t.Errorf("expected agentName to be empty, got %q", cfg.agentName)
	}
}

func TestWithAWSConfig(t *testing.T) {
	awsCfg := aws.Config{Region: "us-west-2"}
	cfg := defaultRuntimeConfig()

	opt := WithAWSConfig(awsCfg)
	if err := opt(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.awsCfg == nil {
		t.Fatal("expected awsCfg to be set")
	}
	if cfg.awsCfg.Region != "us-west-2" {
		t.Errorf("expected region us-west-2, got %q", cfg.awsCfg.Region)
	}
}

func TestWithAgentName(t *testing.T) {
	cfg := defaultRuntimeConfig()

	opt := WithAgentName("my-agent")
	if err := opt(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.agentName != "my-agent" {
		t.Errorf("expected agentName 'my-agent', got %q", cfg.agentName)
	}
}

func TestWithHeartbeatInterval(t *testing.T) {
	t.Run("positive duration succeeds", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		opt := WithHeartbeatInterval(10 * time.Second)
		if err := opt(&cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.heartbeatInterval != 10*time.Second {
			t.Errorf("expected 10s, got %v", cfg.heartbeatInterval)
		}
	})

	t.Run("zero duration returns error", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		opt := WithHeartbeatInterval(0)
		err := opt(&cfg)
		if err != ErrHeartbeatInterval {
			t.Errorf("expected ErrHeartbeatInterval, got %v", err)
		}
	})

	t.Run("negative duration returns error", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		opt := WithHeartbeatInterval(-1 * time.Second)
		err := opt(&cfg)
		if err != ErrHeartbeatInterval {
			t.Errorf("expected ErrHeartbeatInterval, got %v", err)
		}
	})
}

func TestWithShutdownTimeout(t *testing.T) {
	t.Run("positive duration succeeds", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		opt := WithShutdownTimeout(60 * time.Second)
		if err := opt(&cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.shutdownTimeout != 60*time.Second {
			t.Errorf("expected 60s, got %v", cfg.shutdownTimeout)
		}
	})

	t.Run("zero duration returns error", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		opt := WithShutdownTimeout(0)
		err := opt(&cfg)
		if err != ErrShutdownTimeout {
			t.Errorf("expected ErrShutdownTimeout, got %v", err)
		}
	})

	t.Run("negative duration returns error", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		opt := WithShutdownTimeout(-5 * time.Second)
		err := opt(&cfg)
		if err != ErrShutdownTimeout {
			t.Errorf("expected ErrShutdownTimeout, got %v", err)
		}
	})
}

func TestWithStreaming(t *testing.T) {
	t.Run("disable streaming", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		opt := WithStreaming(false)
		if err := opt(&cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.streaming {
			t.Error("expected streaming to be false")
		}
	})

	t.Run("enable streaming", func(t *testing.T) {
		cfg := defaultRuntimeConfig()
		cfg.streaming = false
		opt := WithStreaming(true)
		if err := opt(&cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.streaming {
			t.Error("expected streaming to be true")
		}
	})
}

func TestWithMaxConcurrency(t *testing.T) {
	cfg := defaultRuntimeConfig()
	opt := WithMaxConcurrency(20)
	if err := opt(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.maxConcurrency != 20 {
		t.Errorf("expected maxConcurrency 20, got %d", cfg.maxConcurrency)
	}
}

func TestWithAutoConversation(t *testing.T) {
	cfg := defaultRuntimeConfig()
	opt := WithAutoConversation()
	if err := opt(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.autoConversation {
		t.Error("expected autoConversation to be true")
	}
}
