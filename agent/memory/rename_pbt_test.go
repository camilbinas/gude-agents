package memory

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/rag"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Property 4: Error prefix consistency (memory-package-rename)
// ---------------------------------------------------------------------------

// Feature: memory-package-rename, Property 4: Error prefix consistency
//
// For any error returned by memory package validation, the prefix SHALL be
// "memory:"; for redis memory it SHALL be "redis memory:"; for agentcore
// memory it SHALL be "agentcore memory:".
//
// The test has two parts:
//   - Part A: Directly invoke Adapter.Remember and Adapter.Recall with
//     randomly generated invalid inputs and verify every returned error
//     starts with "memory:".
//   - Part B: Scan source files in the redis and agentcore submodules for
//     error string literals and verify they use the correct prefix.
//
// **Validates: Requirements 14.1, 14.2, 14.3, 14.4**
func TestProperty_ErrorPrefixConsistency(t *testing.T) {
	t.Run("MemoryAdapterErrors", testMemoryAdapterErrorPrefix)
	t.Run("SourceFileErrorPrefixes", testSourceFileErrorPrefixes)
}

// testMemoryAdapterErrorPrefix uses rapid to generate random invalid inputs
// for Adapter.Remember and Adapter.Recall, then asserts every returned error
// message starts with "memory:".
func testMemoryAdapterErrorPrefix(t *testing.T) {
	// Build a real Adapter backed by an in-memory store.
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	adapter := NewAdapter(scopedStore, &hashEmbedder{dim: 8})

	// errorCase describes one way to provoke a validation error.
	type errorCase struct {
		name string
		fn   func(ctx context.Context) error
	}

	rapid.Check(t, func(rt *rapid.T) {
		// Generate random strings to use as the "other" valid parameter so
		// the test isn't always using the same static values.
		randomFact := rapid.StringMatching(`[a-zA-Z0-9]{1,50}`).Draw(rt, "fact")
		randomQuery := rapid.StringMatching(`[a-zA-Z0-9]{1,50}`).Draw(rt, "query")
		randomID := rapid.StringMatching(`[a-zA-Z0-9]{1,50}`).Draw(rt, "identifier")
		badLimit := rapid.IntRange(-100, 0).Draw(rt, "badLimit")

		cases := []errorCase{
			{
				name: "Remember_emptyIdentifier",
				fn: func(ctx context.Context) error {
					return adapter.Remember(ctx, "", randomFact, nil)
				},
			},
			{
				name: "Remember_emptyFact",
				fn: func(ctx context.Context) error {
					return adapter.Remember(ctx, randomID, "", nil)
				},
			},
			{
				name: "Recall_emptyIdentifier",
				fn: func(ctx context.Context) error {
					_, err := adapter.Recall(ctx, "", randomQuery, 5)
					return err
				},
			},
			{
				name: "Recall_invalidLimit",
				fn: func(ctx context.Context) error {
					_, err := adapter.Recall(ctx, randomID, randomQuery, badLimit)
					return err
				},
			},
		}

		// Pick a random error case each iteration.
		idx := rapid.IntRange(0, len(cases)-1).Draw(rt, "caseIndex")
		tc := cases[idx]

		err := tc.fn(context.Background())
		if err == nil {
			rt.Fatalf("%s: expected error, got nil", tc.name)
		}

		const wantPrefix = "memory:"
		if !strings.HasPrefix(err.Error(), wantPrefix) {
			rt.Fatalf("%s: error %q does not start with %q",
				tc.name, err.Error(), wantPrefix)
		}
	})
}

// errorLiteralRE matches Go error string literals in errors.New("...") and
// fmt.Errorf("...") calls, capturing the prefix before the first colon.
var errorLiteralRE = regexp.MustCompile(`(?:errors\.New|fmt\.Errorf)\("([^"]+)"`)

// collectErrorPrefixes scans .go files (non-test) in dir for error string
// literals and returns the set of prefixes (text before the first ": ").
// When recursive is false, only files directly in dir are scanned (no
// subdirectories). When true, the entire subtree is walked.
func collectErrorPrefixes(t *testing.T, dir string, recursive bool) []string {
	t.Helper()

	var files []string

	if recursive {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed to walk %s: %v", dir, err)
		}
	} else {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("failed to read directory %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}

	var prefixes []string
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("failed to open %s: %v", path, err)
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			matches := errorLiteralRE.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				literal := m[1]
				// Extract the prefix: everything before the first ": ".
				if idx := strings.Index(literal, ": "); idx >= 0 {
					prefixes = append(prefixes, literal[:idx])
				}
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("error reading %s: %v", path, err)
		}
		f.Close()
	}

	return prefixes
}

// testSourceFileErrorPrefixes scans source files in the memory, redis, and
// agentcore packages and verifies that every error literal uses the correct
// prefix for its package.
func testSourceFileErrorPrefixes(t *testing.T) {
	type prefixSpecEntry struct {
		dir        string
		wantPrefix string
		recursive  bool
	}

	specs := []prefixSpecEntry{
		// Root memory package: only scan direct files (not redis/ or agentcore/).
		{dir: ".", wantPrefix: "memory", recursive: false},
		// Redis submodule: scan recursively (single directory anyway).
		{dir: "redis", wantPrefix: "redis memory", recursive: true},
		// AgentCore submodule: scan recursively.
		{dir: "agentcore", wantPrefix: "agentcore memory", recursive: true},
	}

	// Collect all (dir, prefix, actual) triples.
	type entry struct {
		dir    string
		want   string
		actual string
	}

	var allEntries []entry
	for _, s := range specs {
		prefixes := collectErrorPrefixes(t, s.dir, s.recursive)
		for _, p := range prefixes {
			allEntries = append(allEntries, entry{
				dir:    s.dir,
				want:   s.wantPrefix,
				actual: p,
			})
		}
	}

	if len(allEntries) == 0 {
		t.Fatal("no error prefixes found in source files; expected at least one per package")
	}

	rapid.Check(t, func(rt *rapid.T) {
		idx := rapid.IntRange(0, len(allEntries)-1).Draw(rt, "entryIndex")
		e := allEntries[idx]

		if e.actual != e.want {
			rt.Fatalf("dir %s: error prefix %q does not match expected %q",
				e.dir, e.actual, e.want)
		}
	})
}
