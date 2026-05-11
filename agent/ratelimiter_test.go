package agent

import (
	"testing"
)

func TestNewRateLimiter_BothZeroReturnsError(t *testing.T) {
	rl, err := NewRateLimiter(0, 0)
	if err == nil {
		t.Fatal("expected error when both rpmLimit and tpmLimit are zero, got nil")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter when both limits are zero")
	}
}

func TestNewRateLimiter_RPMOnlySucceeds(t *testing.T) {
	rl, err := NewRateLimiter(10, 0)
	if err != nil {
		t.Fatalf("unexpected error for RPM-only limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.rpmLimit != 10 {
		t.Errorf("expected rpmLimit=10, got %d", rl.rpmLimit)
	}
	if rl.tpmLimit != 0 {
		t.Errorf("expected tpmLimit=0, got %d", rl.tpmLimit)
	}
}

func TestNewRateLimiter_TPMOnlySucceeds(t *testing.T) {
	rl, err := NewRateLimiter(0, 1000)
	if err != nil {
		t.Fatalf("unexpected error for TPM-only limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.rpmLimit != 0 {
		t.Errorf("expected rpmLimit=0, got %d", rl.rpmLimit)
	}
	if rl.tpmLimit != 1000 {
		t.Errorf("expected tpmLimit=1000, got %d", rl.tpmLimit)
	}
}

func TestNewRateLimiter_BothLimitsSucceeds(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000)
	if err != nil {
		t.Fatalf("unexpected error for both-limits limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.rpmLimit != 10 {
		t.Errorf("expected rpmLimit=10, got %d", rl.rpmLimit)
	}
	if rl.tpmLimit != 1000 {
		t.Errorf("expected tpmLimit=1000, got %d", rl.tpmLimit)
	}
}

func TestNewRateLimiter_DefaultStrategySlidingWindow(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.windowStrategy != SlidingWindow {
		t.Errorf("expected default windowStrategy=SlidingWindow, got %d", rl.windowStrategy)
	}
}

func TestNewRateLimiter_DefaultOverflowBlockMode(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.overflowBehavior != BlockMode {
		t.Errorf("expected default overflowBehavior=BlockMode, got %d", rl.overflowBehavior)
	}
}

func TestNewRateLimiter_WithFixedWindowOption(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000, WithFixedWindow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.windowStrategy != FixedWindow {
		t.Errorf("expected windowStrategy=FixedWindow, got %d", rl.windowStrategy)
	}
}

func TestNewRateLimiter_WithFailFastOption(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000, WithFailFast())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.overflowBehavior != FailFastMode {
		t.Errorf("expected overflowBehavior=FailFastMode, got %d", rl.overflowBehavior)
	}
}
