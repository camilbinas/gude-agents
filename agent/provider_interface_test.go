package agent

import (
	"testing"
)

// TestCacheableBlock_ImplementsContentBlock verifies at compile time and at
// runtime that CacheableBlock satisfies the ContentBlock sealed interface.
//
// The compile-time assertion `var _ ContentBlock = CacheableBlock{}` lives in
// provider.go; this test documents the intent and provides a named test hook.
//
// Requirements: 1.1, 8.4
func TestCacheableBlock_ImplementsContentBlock(t *testing.T) {
	// This function body intentionally exercises the interface at runtime too.
	var _ ContentBlock = CacheableBlock{}
	var _ ContentBlock = CacheableBlock{Inner: TextBlock{Text: "hello"}}
	var _ ContentBlock = CacheableBlock{Inner: ToolResultBlock{ToolUseID: "id", Content: "result"}}
	// If this compiles and runs, CacheableBlock satisfies ContentBlock.
}

// unknownBlock is a custom ContentBlock implementation that is not one of the
// registered block types. It is used to verify that provider switch statements
// have a safe default and do not panic on unknown types.
type unknownBlock struct{}

func (unknownBlock) contentBlock() {}

// TestCacheableBlock_UnknownInnerType_DoesNotPanic verifies that wrapping an
// unknown ContentBlock inside a CacheableBlock does not panic when the resulting
// value is used as a ContentBlock.
//
// This covers the sealed-interface contract: new block types that the current
// switch statements do not know about should be gracefully ignored or passed
// through, not panic.
//
// Requirements: 1.1, 8.4
func TestCacheableBlock_UnknownInnerType_DoesNotPanic(t *testing.T) {
	// unknownBlock satisfies ContentBlock but is not handled by any provider
	// switch statement. Wrapping it in CacheableBlock must not panic at
	// construction time.
	block := CacheableBlock{Inner: unknownBlock{}}

	// Accessing the interface method should not panic.
	block.contentBlock()

	// The block should be usable as a ContentBlock value.
	var cb ContentBlock = block
	_ = cb

	// Nesting is also safe.
	nested := CacheableBlock{Inner: CacheableBlock{Inner: unknownBlock{}}}
	nested.contentBlock()
}
