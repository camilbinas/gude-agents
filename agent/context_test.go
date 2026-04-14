package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestInvocationContext_SetGet(t *testing.T) {
	ic := NewInvocationContext()

	ic.Set("key1", "value1")
	ic.Set(42, true)

	v, ok := ic.Get("key1")
	if !ok || v != "value1" {
		t.Fatalf("expected (value1, true), got (%v, %v)", v, ok)
	}

	v, ok = ic.Get(42)
	if !ok || v != true {
		t.Fatalf("expected (true, true), got (%v, %v)", v, ok)
	}
}

func TestInvocationContext_GetNonExistent(t *testing.T) {
	ic := NewInvocationContext()

	v, ok := ic.Get("missing")
	if ok || v != nil {
		t.Fatalf("expected (nil, false), got (%v, %v)", v, ok)
	}
}

func TestInvocationContext_OverwriteKey(t *testing.T) {
	ic := NewInvocationContext()

	ic.Set("key", "first")
	ic.Set("key", "second")

	v, ok := ic.Get("key")
	if !ok || v != "second" {
		t.Fatalf("expected (second, true), got (%v, %v)", v, ok)
	}
}

func TestInvocationContext_ConcurrentAccess(t *testing.T) {
	ic := NewInvocationContext()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent writers
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			ic.Set(fmt.Sprintf("key-%d", i), i)
		}(i)
	}

	// Concurrent readers
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			ic.Get(fmt.Sprintf("key-%d", i))
		}(i)
	}

	wg.Wait()

	// Verify all writes landed
	for i := range goroutines {
		v, ok := ic.Get(fmt.Sprintf("key-%d", i))
		if !ok || v != i {
			t.Errorf("key-%d: expected (%d, true), got (%v, %v)", i, i, v, ok)
		}
	}
}

func TestGetInvocationContext_NilWhenNoneAttached(t *testing.T) {
	ctx := context.Background()

	ic := GetInvocationContext(ctx)
	if ic != nil {
		t.Fatalf("expected nil, got %v", ic)
	}
}

func TestWithInvocationContext_RoundTrip(t *testing.T) {
	ic := NewInvocationContext()
	ic.Set("hello", "world")

	ctx := WithInvocationContext(context.Background(), ic)
	got := GetInvocationContext(ctx)

	if got != ic {
		t.Fatal("expected same InvocationContext instance")
	}

	v, ok := got.Get("hello")
	if !ok || v != "world" {
		t.Fatalf("expected (world, true), got (%v, %v)", v, ok)
	}
}
