package mcp

import "fmt"

const (
	// DefaultMaxOutputBytes caps the text an MCP tool call may return.
	DefaultMaxOutputBytes = 64 * 1024
	// DefaultMaxImages caps the number of images kept per tool call.
	DefaultMaxImages = 4
	// DefaultMaxImageBytes caps the size of each kept image.
	DefaultMaxImageBytes = 5 * 1024 * 1024
	// maxErrorTextBytes caps MCP error text embedded in a Go error.
	maxErrorTextBytes = 2 * 1024
)

// outputLimits bounds what a (possibly hostile) MCP server can push into the
// model context.
type outputLimits struct {
	maxBytes      int
	maxImages     int
	maxImageBytes int
}

func defaultLimits() outputLimits {
	return outputLimits{maxBytes: DefaultMaxOutputBytes, maxImages: DefaultMaxImages, maxImageBytes: DefaultMaxImageBytes}
}

// truncateText cuts s to at most max bytes (on a rune boundary) and appends a
// marker naming the original size.
func truncateText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !runeStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n[truncated: %d of %d bytes shown]", cut, len(s))
}

func runeStart(b byte) bool { return b&0xC0 != 0x80 }
