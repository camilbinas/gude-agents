package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// TestImageSourceValidate_ValidMIMETypes verifies that each of the four
// supported MIME types returns nil from Validate().
func TestImageSourceValidate_ValidMIMETypes(t *testing.T) {
	validTypes := []string{
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/webp",
	}
	for _, mime := range validTypes {
		t.Run(mime, func(t *testing.T) {
			src := ImageSource{MIMEType: mime}
			if err := src.Validate(); err != nil {
				t.Errorf("Validate() returned unexpected error for %q: %v", mime, err)
			}
		})
	}
}

// TestImageSourceValidate_InvalidMIMEType verifies that an invalid MIME type
// returns a non-nil error that contains the invalid string.
func TestImageSourceValidate_InvalidMIMEType(t *testing.T) {
	invalidTypes := []string{
		"image/bmp",
		"image/tiff",
		"application/pdf",
		"text/plain",
		"",
		"image/",
		"jpeg",
	}
	for _, mime := range invalidTypes {
		t.Run(mime, func(t *testing.T) {
			src := ImageSource{MIMEType: mime}
			err := src.Validate()
			if err == nil {
				t.Errorf("Validate() returned nil for invalid MIME type %q, expected error", mime)
				return
			}
			if !strings.Contains(err.Error(), mime) {
				t.Errorf("error %q does not contain invalid MIME type %q", err.Error(), mime)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Loop image propagation tests
// ---------------------------------------------------------------------------

// TestLoop_NoImages_SingleTextBlock verifies that when no images are attached,
// the first user message contains exactly one TextBlock (backward compatibility).
func TestLoop_NoImages_SingleTextBlock(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "ok"})
	a, err := New(cp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) == 0 {
		t.Fatal("provider was never called")
	}

	firstMsg := cp.captured[0].Messages[0]
	if len(firstMsg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(firstMsg.Content))
	}
	tb, ok := firstMsg.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", firstMsg.Content[0])
	}
	if tb.Text != "hello" {
		t.Errorf("expected TextBlock.Text=%q, got %q", "hello", tb.Text)
	}
}

// TestLoop_WithImages_PrependsImagesThenText verifies that when images are
// attached via WithImages, the first user message content is [ImageBlock,
// ImageBlock, TextBlock].
func TestLoop_WithImages_PrependsImagesThenText(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "ok"})
	a, err := New(cp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	img1 := ImageBlock{Source: ImageSource{MIMEType: "image/jpeg", Data: []byte{0xFF, 0xD8}}}
	img2 := ImageBlock{Source: ImageSource{MIMEType: "image/png", Data: []byte{0x89, 0x50}}}
	images := []ImageBlock{img1, img2}

	ctx := WithImages(context.Background(), images)
	_, _, err = a.Invoke(ctx, "describe these")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) == 0 {
		t.Fatal("provider was never called")
	}

	firstMsg := cp.captured[0].Messages[0]
	content := firstMsg.Content

	// Expect [ImageBlock, ImageBlock, TextBlock].
	if len(content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(content))
	}

	got1, ok := content[0].(ImageBlock)
	if !ok {
		t.Fatalf("content[0]: expected ImageBlock, got %T", content[0])
	}
	if !reflect.DeepEqual(got1, img1) {
		t.Errorf("content[0]: expected %+v, got %+v", img1, got1)
	}

	got2, ok := content[1].(ImageBlock)
	if !ok {
		t.Fatalf("content[1]: expected ImageBlock, got %T", content[1])
	}
	if !reflect.DeepEqual(got2, img2) {
		t.Errorf("content[1]: expected %+v, got %+v", img2, got2)
	}

	tb, ok := content[2].(TextBlock)
	if !ok {
		t.Fatalf("content[2]: expected TextBlock, got %T", content[2])
	}
	if tb.Text != "describe these" {
		t.Errorf("TextBlock.Text: expected %q, got %q", "describe these", tb.Text)
	}
}

// panicProvider panics if called — used to verify the provider is never reached.
type panicProvider struct{}

func (panicProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	panic("panicProvider.Converse called — should not have reached provider")
}

func (panicProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	panic("panicProvider.ConverseStream called — should not have reached provider")
}

// TestLoop_InvalidMIMEType_ReturnsErrorBeforeProvider verifies that when an
// ImageBlock with an invalid MIME type is attached, the loop returns an error
// before calling the provider.
func TestLoop_InvalidMIMEType_ReturnsErrorBeforeProvider(t *testing.T) {
	a, err := New(panicProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	badImage := ImageBlock{Source: ImageSource{MIMEType: "image/bmp", Data: []byte{0x42, 0x4D}}}
	ctx := WithImages(context.Background(), []ImageBlock{badImage})

	_, _, invokeErr := a.Invoke(ctx, "hello")
	if invokeErr == nil {
		t.Fatal("expected error for invalid MIME type, got nil")
	}
	if !strings.Contains(invokeErr.Error(), "image/bmp") {
		t.Errorf("expected error to contain %q, got: %v", "image/bmp", invokeErr)
	}
}
