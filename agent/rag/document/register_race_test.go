package document

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestRegisterParser_ConcurrentNoRace exercises RegisterParser concurrently
// with parser lookups (via a loadConfig.parserFor call, the same read path
// LoadFiles uses). Run with -race, it fails if the defaultParsers map is
// accessed without synchronization.
func TestRegisterParser_ConcurrentNoRace(t *testing.T) {
	stub := ParserFunc(func(_ context.Context, _ string) (string, error) { return "", nil })
	cfg := newLoadConfig(nil)

	var wg sync.WaitGroup
	const workers = 8
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ext := fmt.Sprintf(".race%d", n)
			for j := 0; j < 200; j++ {
				RegisterParser(ext, stub)
				// Read through the same lookup path LoadFiles uses.
				_, _ = cfg.parserFor(ext)
				_, _ = cfg.parserFor(".md")
			}
		}(i)
	}
	wg.Wait()

	// Sanity: the registered extensions are now resolvable.
	for i := 0; i < workers; i++ {
		if _, ok := cfg.parserFor(fmt.Sprintf(".race%d", i)); !ok {
			t.Fatalf("parser for .race%d not registered", i)
		}
	}
}
