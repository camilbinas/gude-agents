package agentcore

import (
	"testing"

	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 6: Malformed event payload rejection

// TestProperty6_MalformedEventPayloadRejection verifies that for any event payload
// missing at least one required field (empty EventID, empty SessionID, or empty Message),
// the runtime discards the event without panicking and without invoking the agent.
//
// **Validates: Requirements 3.8**
func TestProperty6_MalformedEventPayloadRejection(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an event where at least one required field is empty.
		// Strategy: generate all three fields, then randomly blank at least one.
		eventID := rapid.String().Draw(t, "eventID")
		sessionID := rapid.String().Draw(t, "sessionID")
		message := rapid.String().Draw(t, "message")

		// Choose which fields to make empty (at least one must be empty).
		// Use a bitmask: bit 0 = EventID empty, bit 1 = SessionID empty, bit 2 = Message empty.
		// Valid masks are 1-7 (at least one bit set).
		mask := rapid.IntRange(1, 7).Draw(t, "emptyFieldMask")

		if mask&1 != 0 {
			eventID = ""
		}
		if mask&2 != 0 {
			sessionID = ""
		}
		if mask&4 != 0 {
			message = ""
		}

		ev := incomingEvent{
			EventID:   eventID,
			SessionID: sessionID,
			Message:   message,
		}

		// The event must fail validation (be discarded).
		if ev.validate() {
			t.Fatalf("expected validate() to return false for malformed event: %+v (mask=%d)", ev, mask)
		}
	})
}

// TestProperty6_ValidEventAccepted verifies that for any event with all required
// fields non-empty, validate() returns true (the event would be processed).
//
// **Validates: Requirements 3.8**
func TestProperty6_ValidEventAccepted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate non-empty strings for all required fields.
		eventID := rapid.StringMatching(`.+`).Draw(t, "eventID")
		sessionID := rapid.StringMatching(`.+`).Draw(t, "sessionID")
		message := rapid.StringMatching(`.+`).Draw(t, "message")

		// Optional timestamp can be anything.
		timestamp := rapid.String().Draw(t, "timestamp")

		ev := incomingEvent{
			EventID:   eventID,
			SessionID: sessionID,
			Message:   message,
			Timestamp: timestamp,
		}

		// The event must pass validation.
		if !ev.validate() {
			t.Fatalf("expected validate() to return true for valid event: %+v", ev)
		}
	})
}
