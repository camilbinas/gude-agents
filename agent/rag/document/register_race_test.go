package document

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestRegisterParser_ConcurrentNoRace runs RegisterParser concurrently with
// lookups; under -race it fails if defaultParsers is accessed unsynchronized.
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
				_, _ = cfg.parserFor(ext)
				_, _ = cfg.parserFor(".md")
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if _, ok := cfg.parserFor(fmt.Sprintf(".race%d", i)); !ok {
			t.Fatalf("parser for .race%d not registered", i)
		}
	}
}
