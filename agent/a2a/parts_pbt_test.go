package a2a

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"pgregory.net/rapid"
)

// **Validates: Requirements 1.4**

// TestProperty_InboundMixedMessagePartitioning verifies that ConvertInbound correctly
// partitions a message containing an arbitrary mix of TextParts, image DataParts/FileParts,
// and document DataParts/FileParts into the separate fields of InboundResult with the
// correct counts.
func TestProperty_InboundMixedMessagePartitioning(t *testing.T) {
	logger := slog.Default()

	rapid.Check(t, func(t *rapid.T) {
		// Generate random counts for each part type.
		numText := rapid.IntRange(0, 5).Draw(t, "numText")
		numImages := rapid.IntRange(0, 5).Draw(t, "numImages")
		numDocs := rapid.IntRange(0, 5).Draw(t, "numDocs")

		// Ensure at least one part exists so the message is non-trivial.
		if numText+numImages+numDocs == 0 {
			numText = 1
		}

		imageMIMEs := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
		docMIMEs := []string{"application/pdf", "text/plain", "text/html", "text/csv", "text/markdown"}

		// We build parts in a known order and track which are text parts.
		// After permutation, we derive the expected text from the message order.
		type taggedPart struct {
			part *a2a.Part
			text string // non-empty only for text parts
		}

		var tagged []taggedPart

		// Generate text parts.
		for i := 0; i < numText; i++ {
			text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, "textContent")
			tagged = append(tagged, taggedPart{part: a2a.NewTextPart(text), text: text})
		}

		// Generate image parts (mix of Raw and URL).
		for i := 0; i < numImages; i++ {
			mime := rapid.SampledFrom(imageMIMEs).Draw(t, "imageMIME")
			useURL := rapid.Bool().Draw(t, "imageUseURL")
			if useURL {
				url := rapid.StringMatching(`https://example\\.com/img/[a-z]{3,10}\\.png`).Draw(t, "imageURL")
				tagged = append(tagged, taggedPart{part: a2a.NewFileURLPart(a2a.URL(url), mime)})
			} else {
				raw := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "imageRaw")
				p := a2a.NewRawPart(raw)
				p.MediaType = mime
				tagged = append(tagged, taggedPart{part: p})
			}
		}

		// Generate document parts (mix of Raw and URL).
		for i := 0; i < numDocs; i++ {
			mime := rapid.SampledFrom(docMIMEs).Draw(t, "docMIME")
			useURL := rapid.Bool().Draw(t, "docUseURL")
			if useURL {
				url := rapid.StringMatching(`https://example\\.com/docs/[a-z]{3,10}\\.pdf`).Draw(t, "docURL")
				tagged = append(tagged, taggedPart{part: a2a.NewFileURLPart(a2a.URL(url), mime)})
			} else {
				raw := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "docRaw")
				p := a2a.NewRawPart(raw)
				p.MediaType = mime
				tagged = append(tagged, taggedPart{part: p})
			}
		}

		// Shuffle to simulate arbitrary ordering.
		reordered := rapid.Permutation(tagged).Draw(t, "perm")

		// Build the message parts and derive expected text in message order.
		msgParts := make([]*a2a.Part, len(reordered))
		var expectedTexts []string
		for i, tp := range reordered {
			msgParts[i] = tp.part
			if tp.text != "" {
				expectedTexts = append(expectedTexts, tp.text)
			}
		}

		msg := a2a.NewMessage(a2a.MessageRoleUser, msgParts...)

		result := ConvertInbound(msg, logger)

		// (a) Text equals the concatenation of all TextPart values joined by newlines,
		// in the order they appear in the message.
		expectedText := strings.Join(expectedTexts, "\n")
		if result.Text != expectedText {
			t.Fatalf("Text mismatch:\n  got:      %q\n  expected: %q", result.Text, expectedText)
		}

		// (b) Images contains exactly the blocks derived from parts with image MIME types.
		if len(result.Images) != numImages {
			t.Fatalf("Images count mismatch: got %d, expected %d", len(result.Images), numImages)
		}

		// (c) Documents contains exactly the blocks derived from parts with document MIME types.
		if len(result.Documents) != numDocs {
			t.Fatalf("Documents count mismatch: got %d, expected %d", len(result.Documents), numDocs)
		}
	})
}

// **Validates: Requirements 1.5**

// TestProperty_UnrecognizedMIMETypesExcluded verifies that ConvertInbound excludes
// parts with MIME types not in the recognized imageMIMETypes or docMIMETypes maps.
// The function should not return an error and should produce no Images or Documents
// for those parts.
func TestProperty_UnrecognizedMIMETypesExcluded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-5 parts, all with unrecognized MIME types.
		numParts := rapid.IntRange(1, 5).Draw(t, "numParts")

		unrecognizedMIMEs := []string{
			"application/octet-stream",
			"application/json",
			"application/xml",
			"application/zip",
			"audio/mpeg",
			"audio/wav",
			"video/mp4",
			"video/webm",
			"font/woff2",
			"model/gltf+json",
			"multipart/form-data",
		}

		var parts []*a2a.Part
		for i := 0; i < numParts; i++ {
			mime := rapid.SampledFrom(unrecognizedMIMEs).Draw(t, "unrecognizedMIME")

			// Randomly choose between Raw content and URL content.
			useRaw := rapid.Bool().Draw(t, "useRaw")
			if useRaw {
				data := rapid.SliceOfN(rapid.Byte(), 1, 100).Draw(t, "rawData")
				p := a2a.NewRawPart(data)
				p.MediaType = mime
				parts = append(parts, p)
			} else {
				url := rapid.StringMatching(`https://example\.com/file-[a-z0-9]{4,8}`).Draw(t, "url")
				p := &a2a.Part{
					Content:   a2a.URL(url),
					MediaType: mime,
				}
				parts = append(parts, p)
			}
		}

		msg := a2a.NewMessage(a2a.MessageRoleUser, parts...)
		logger := slog.Default()

		result := ConvertInbound(msg, logger)

		// All parts had unrecognized MIME types, so no images or documents should be produced.
		if len(result.Images) != 0 {
			t.Fatalf("expected 0 images for unrecognized MIME types, got %d", len(result.Images))
		}
		if len(result.Documents) != 0 {
			t.Fatalf("expected 0 documents for unrecognized MIME types, got %d", len(result.Documents))
		}
		// Text should also be empty since none of the parts are TextParts.
		if result.Text != "" {
			t.Fatalf("expected empty text for non-text unrecognized parts, got %q", result.Text)
		}
	})
}
