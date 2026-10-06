package main

import (
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestValidateResumeVersionRejectsStaleBrowserDecision(t *testing.T) {
	// The browser observed pause v2, another client resumed it, and the same
	// execution later paused at v4. The old decision must not reach Resume.
	current := &agent.Interrupt{ExecutionID: "execution-1", ExecutionVersion: 4}
	if err := validateResumeVersion(current, 2); err == nil || !strings.Contains(err.Error(), "stale execution version") {
		t.Fatalf("stale decision error = %v", err)
	}
	if current.ExecutionVersion != 4 {
		t.Fatal("version guard mutated the current pause")
	}
	if err := validateResumeVersion(current, 4); err != nil {
		t.Fatalf("current decision rejected: %v", err)
	}
}
