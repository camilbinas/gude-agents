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

func TestGetInferenceConfig_NilWhenNoneAttached(t *testing.T) {
	ctx := context.Background()

	cfg := GetInferenceConfig(ctx)
	if cfg != nil {
		t.Fatalf("expected nil, got %+v", cfg)
	}
}

func TestWithInferenceConfig_RoundTrip(t *testing.T) {
	temp := 0.7
	topP := 0.9
	topK := 50
	maxTok := 1024
	cfg := &InferenceConfig{
		Temperature:   &temp,
		TopP:          &topP,
		TopK:          &topK,
		StopSequences: []string{"STOP", "END"},
		MaxTokens:     &maxTok,
	}

	ctx := WithInferenceConfig(context.Background(), cfg)
	got := GetInferenceConfig(ctx)

	if got != cfg {
		t.Fatal("expected same InferenceConfig pointer")
	}
	if *got.Temperature != temp {
		t.Errorf("Temperature: expected %f, got %f", temp, *got.Temperature)
	}
	if *got.TopP != topP {
		t.Errorf("TopP: expected %f, got %f", topP, *got.TopP)
	}
	if *got.TopK != topK {
		t.Errorf("TopK: expected %d, got %d", topK, *got.TopK)
	}
	if *got.MaxTokens != maxTok {
		t.Errorf("MaxTokens: expected %d, got %d", maxTok, *got.MaxTokens)
	}
	if len(got.StopSequences) != 2 || got.StopSequences[0] != "STOP" || got.StopSequences[1] != "END" {
		t.Errorf("StopSequences: expected [STOP END], got %v", got.StopSequences)
	}
}

func TestGetImages_NilWhenNoneAttached(t *testing.T) {
	ctx := context.Background()

	images := GetImages(ctx)
	if images != nil {
		t.Fatalf("expected nil, got %v", images)
	}
}

func TestWithImages_RoundTrip(t *testing.T) {
	want := []ImageBlock{
		{Source: ImageSource{MIMEType: "image/jpeg", Data: []byte{0xFF, 0xD8}}},
		{Source: ImageSource{MIMEType: "image/png", Data: []byte{0x89, 0x50}}},
	}

	ctx := WithImages(context.Background(), want)
	got := GetImages(ctx)

	if len(got) != len(want) {
		t.Fatalf("expected %d images, got %d", len(want), len(got))
	}
	for i, img := range got {
		if img.Source.MIMEType != want[i].Source.MIMEType {
			t.Errorf("image[%d] MIMEType: expected %q, got %q", i, want[i].Source.MIMEType, img.Source.MIMEType)
		}
	}
}

func TestWithImages_NilSlice(t *testing.T) {
	ctx := WithImages(context.Background(), nil)

	images := GetImages(ctx)
	if images != nil {
		t.Fatalf("expected nil, got %v", images)
	}
}

func TestWithImages_EmptySlice(t *testing.T) {
	ctx := WithImages(context.Background(), []ImageBlock{})

	images := GetImages(ctx)
	if images != nil {
		t.Fatalf("expected nil, got %v", images)
	}
}

func TestWithImages_ChainedCallsInnermostWins(t *testing.T) {
	outer := []ImageBlock{
		{Source: ImageSource{MIMEType: "image/gif", Data: []byte{0x47}}},
	}
	inner := []ImageBlock{
		{Source: ImageSource{MIMEType: "image/webp", Data: []byte{0x52}}},
		{Source: ImageSource{MIMEType: "image/png", Data: []byte{0x89}}},
	}

	ctx := WithImages(context.Background(), outer)
	ctx = WithImages(ctx, inner)

	got := GetImages(ctx)
	if len(got) != len(inner) {
		t.Fatalf("expected %d images from innermost call, got %d", len(inner), len(got))
	}
	for i, img := range got {
		if img.Source.MIMEType != inner[i].Source.MIMEType {
			t.Errorf("image[%d] MIMEType: expected %q, got %q", i, inner[i].Source.MIMEType, img.Source.MIMEType)
		}
	}
}

func TestWithIdentifier_RoundTrip(t *testing.T) {
	ctx := WithIdentifier(context.Background(), "user-42")

	got := GetIdentifier(ctx)
	if got != "user-42" {
		t.Fatalf("expected %q, got %q", "user-42", got)
	}
}

func TestGetTokenUsage_FalseWhenNoneAttached(t *testing.T) {
	ctx := context.Background()

	_, ok := GetTokenUsage(ctx)
	if ok {
		t.Fatal("expected ok=false when no token usage attached")
	}
}

func TestWithTokenUsage_RoundTrip(t *testing.T) {
	usage := TokenUsage{InputTokens: 1500, OutputTokens: 300}

	ctx := WithTokenUsage(context.Background(), usage)
	got, ok := GetTokenUsage(ctx)

	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.InputTokens != 1500 {
		t.Errorf("InputTokens: expected 1500, got %d", got.InputTokens)
	}
	if got.OutputTokens != 300 {
		t.Errorf("OutputTokens: expected 300, got %d", got.OutputTokens)
	}
}

func TestWithTokenUsage_ValueSemantics(t *testing.T) {
	usage := TokenUsage{InputTokens: 100, OutputTokens: 50}
	ctx := WithTokenUsage(context.Background(), usage)

	// Mutating the original should not affect what's in context.
	usage.InputTokens = 9999

	got, ok := GetTokenUsage(ctx)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.InputTokens != 100 {
		t.Errorf("expected 100 (value copy), got %d", got.InputTokens)
	}
}

func TestGetIdentifier_EmptyWhenNoneAttached(t *testing.T) {
	got := GetIdentifier(context.Background())
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestWithIdentifier_EmptyStringIgnored(t *testing.T) {
	ctx := WithIdentifier(context.Background(), "")

	got := GetIdentifier(ctx)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
