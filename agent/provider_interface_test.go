package agent

import (
	"testing"
)

// unknownBlock is a custom ContentBlock implementation that is not one of the
// registered block types. It is used to verify that provider switch statements
// have a safe default and do not panic on unknown types.
type unknownBlock struct{}

func (unknownBlock) contentBlock() {}

// TestKnownContentBlocks_AllImplementInterface verifies that all known ContentBlock
// types satisfy the sealed interface at compile time and runtime.
//
// Requirements: 1.1
func TestKnownContentBlocks_AllImplementInterface(t *testing.T) {
	var _ ContentBlock = TextBlock{Text: "hello"}
	var _ ContentBlock = ToolUseBlock{ToolUseID: "id", Name: "tool"}
	var _ ContentBlock = ToolResultBlock{ToolUseID: "id", Content: "result"}
	// If this compiles and runs, all known ContentBlock types satisfy the interface.
}
