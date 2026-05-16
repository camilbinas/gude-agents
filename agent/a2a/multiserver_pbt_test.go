package a2a

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"pgregory.net/rapid"
)

// **Validates: Requirements 3.5**

// TestProperty_MultiServerCardURLContainsPrefix verifies that for any registered prefix,
// the derived Agent Card's SupportedInterfaces URL contains that prefix path.
func TestProperty_MultiServerCardURLContainsPrefix(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-4 unique prefixes.
		numAgents := rapid.IntRange(1, 4).Draw(t, "numAgents")

		prefixes := make([]string, 0, numAgents)
		seen := make(map[string]bool)
		for i := 0; i < numAgents; i++ {
			// Generate a valid path prefix: /segment or /segment/segment
			numSegments := rapid.IntRange(1, 3).Draw(t, "numSegments")
			var segments []string
			for j := 0; j < numSegments; j++ {
				seg := rapid.StringMatching(`[a-z][a-z0-9]{1,10}`).Draw(t, "segment")
				segments = append(segments, seg)
			}
			prefix := "/" + strings.Join(segments, "/")

			// Ensure uniqueness.
			if seen[prefix] {
				prefix = prefix + rapid.StringMatching(`[a-z]{1,4}`).Draw(t, "suffix")
			}
			if seen[prefix] {
				// Skip this iteration if still duplicate (very unlikely).
				continue
			}
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}

		if len(prefixes) == 0 {
			return
		}

		// Build registrations with unique prefixes.
		registrations := make([]AgentRegistration, 0, len(prefixes))
		for _, prefix := range prefixes {
			a, err := agent.New(
				&fakeProvider{response: "ok"},
				prompt.Text("Agent at "+prefix),
				nil,
				agent.WithName("agent"+prefix),
			)
			if err != nil {
				t.Fatal(err)
			}
			registrations = append(registrations, AgentRegistration{
				Prefix: prefix,
				Agent:  a,
			})
		}

		ms, err := NewMultiServer(registrations)
		if err != nil {
			t.Fatal(err)
		}

		// Verify each entry's card URL contains the prefix.
		for i, entry := range ms.entries {
			card := entry.server.Card()

			if len(card.SupportedInterfaces) == 0 {
				t.Fatalf("entry %d (prefix %q): expected at least one supported interface", i, entry.prefix)
			}

			iface := card.SupportedInterfaces[0]
			if !strings.Contains(iface.URL, prefixes[i]) {
				t.Fatalf("entry %d: card interface URL %q does not contain prefix %q", i, iface.URL, prefixes[i])
			}
		}
	})
}

// **Validates: Requirements 3.6**

// TestProperty_MultiServerRejectsDuplicatePrefixes verifies that NewMultiServer returns
// a non-nil error when called with registrations containing duplicate prefixes.
func TestProperty_MultiServerRejectsDuplicatePrefixes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 2-5 unique prefixes.
		numPrefixes := rapid.IntRange(2, 5).Draw(t, "numPrefixes")

		prefixes := make([]string, 0, numPrefixes)
		seen := make(map[string]struct{})
		for len(prefixes) < numPrefixes {
			p := rapid.StringMatching(`/[a-z]{2,8}(/[a-z]{2,8})?`).Draw(t, "prefix")
			if _, exists := seen[p]; !exists {
				seen[p] = struct{}{}
				prefixes = append(prefixes, p)
			}
		}

		// Pick a random index to duplicate.
		dupIdx := rapid.IntRange(0, len(prefixes)-1).Draw(t, "dupIdx")
		dupPrefix := prefixes[dupIdx]

		// Build registrations: all unique prefixes + one duplicate appended at the end.
		regs := make([]AgentRegistration, 0, len(prefixes)+1)
		for _, p := range prefixes {
			a, err := agent.New(
				&fakeProvider{response: "ok"},
				prompt.Text("test agent"),
				nil,
				agent.WithName("agent-"+p),
			)
			if err != nil {
				t.Fatal(err)
			}
			regs = append(regs, AgentRegistration{Prefix: p, Agent: a})
		}

		// Append the duplicate registration.
		dupAgent, err := agent.New(
			&fakeProvider{response: "dup"},
			prompt.Text("dup agent"),
			nil,
			agent.WithName("dup-agent"),
		)
		if err != nil {
			t.Fatal(err)
		}
		regs = append(regs, AgentRegistration{Prefix: dupPrefix, Agent: dupAgent})

		// Shuffle registrations so the duplicate isn't always at the end.
		regs = rapid.Permutation(regs).Draw(t, "regs")

		// NewMultiServer must return an error for duplicate prefixes.
		_, msErr := NewMultiServer(regs)
		if msErr == nil {
			t.Fatalf("expected error for duplicate prefix %q, got nil", dupPrefix)
		}
	})
}

// **Validates: Requirements 3.4**

// TestProperty_MultiServerReturns404ForUnregisteredPrefixes verifies that for any
// prefix NOT in the registered set, a request to that prefix returns 404.
func TestProperty_MultiServerReturns404ForUnregisteredPrefixes(t *testing.T) {
	// Set up a MultiServer with a fixed set of registered prefixes.
	registeredPrefixes := []string{"/agents/alpha", "/agents/beta", "/agents/gamma"}

	regs := make([]AgentRegistration, len(registeredPrefixes))
	for i, prefix := range registeredPrefixes {
		regs[i] = AgentRegistration{
			Prefix: prefix,
			Agent:  newNamedAgent(t, "agent-"+prefix),
		}
	}

	ms, err := NewMultiServer(regs)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(ms.Handler())
	defer ts.Close()

	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random prefix that is guaranteed to NOT match any registered prefix.
		// We use base paths that don't overlap with "/agents/alpha", "/agents/beta", "/agents/gamma".
		unregisteredBase := rapid.SampledFrom([]string{
			"/unknown",
			"/other",
			"/services",
			"/api",
			"/unregistered",
			"/x",
		}).Draw(rt, "base")

		suffix := rapid.StringMatching(`/[a-z]{1,10}`).Draw(rt, "suffix")
		unregisteredPath := unregisteredBase + suffix

		// Also test with well-known card path appended or trailing slash.
		usePath := rapid.SampledFrom([]string{
			unregisteredPath,
			unregisteredPath + "/.well-known/agent-card.json",
			unregisteredPath + "/",
		}).Draw(rt, "pathVariant")

		resp, err := http.Get(ts.URL + usePath)
		if err != nil {
			rt.Fatalf("HTTP request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			rt.Fatalf("expected 404 for unregistered path %q, got %d", usePath, resp.StatusCode)
		}
	})
}

// **Validates: Requirements 3.2, 3.3**

// TestProperty_MultiServerPrefixRoutingCorrectness verifies that for any set of
// unique prefixes registered with a MultiServer, a request to
// {prefix}/.well-known/agent-card.json returns 200 with the correct agent card
// for that prefix.
func TestProperty_MultiServerPrefixRoutingCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-5 unique prefixes.
		numAgents := rapid.IntRange(1, 5).Draw(t, "numAgents")

		// Generate unique prefix segments. Each prefix is /agents/{segment}.
		segments := make(map[string]struct{})
		var prefixes []string
		for len(prefixes) < numAgents {
			seg := rapid.StringMatching(`[a-z][a-z0-9]{2,10}`).Draw(t, "segment")
			if _, exists := segments[seg]; exists {
				continue
			}
			segments[seg] = struct{}{}
			prefixes = append(prefixes, "/agents/"+seg)
		}

		// Build registrations with uniquely named agents.
		registrations := make([]AgentRegistration, len(prefixes))
		for i, prefix := range prefixes {
			name := "agent-" + prefix[len("/agents/"):]
			a, err := agent.New(
				&fakeProvider{response: "ok from " + name},
				prompt.Text("You are "+name),
				nil,
				agent.WithName(name),
			)
			if err != nil {
				t.Fatalf("failed to create agent %q: %v", name, err)
			}
			registrations[i] = AgentRegistration{
				Prefix: prefix,
				Agent:  a,
			}
		}

		// Create MultiServer.
		ms, err := NewMultiServer(registrations)
		if err != nil {
			t.Fatalf("NewMultiServer failed: %v", err)
		}

		ts := httptest.NewServer(ms.Handler())
		defer ts.Close()

		// For each registered prefix, verify the card endpoint returns 200
		// with the correct agent name.
		for i, prefix := range prefixes {
			expectedName := registrations[i].Agent.Name()

			resp, err := http.Get(ts.URL + prefix + "/.well-known/agent-card.json")
			if err != nil {
				t.Fatalf("GET %s/.well-known/agent-card.json failed: %v", prefix, err)
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				t.Fatalf("prefix %q: status = %d, want 200", prefix, resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatalf("prefix %q: failed to read body: %v", prefix, err)
			}

			var card a2a.AgentCard
			if err := json.Unmarshal(body, &card); err != nil {
				t.Fatalf("prefix %q: failed to unmarshal card: %v", prefix, err)
			}

			if card.Name != expectedName {
				t.Fatalf("prefix %q: card.Name = %q, want %q", prefix, card.Name, expectedName)
			}
		}
	})
}
