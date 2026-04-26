package conversation

import (
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

// Thin wrappers around testutil generators to keep existing call sites unchanged.

func genContentBlock(t *rapid.T) agent.ContentBlock  { return testutil.GenContentBlock(t) }
func genMessage(t *rapid.T) agent.Message            { return testutil.GenMessage(t) }
func genMessages(t *rapid.T) []agent.Message         { return testutil.GenMessages(t, 100) }
func genMessagesWithText(t *rapid.T) []agent.Message { return testutil.GenMessagesWithText(t, 100) }
