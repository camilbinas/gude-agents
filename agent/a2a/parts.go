package a2a

import (
	"encoding/base64"
	"log/slog"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
)

// imageMIMETypes is the set of MIME types classified as images.
var imageMIMETypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// docMIMETypes is the set of MIME types classified as documents.
var docMIMETypes = map[string]bool{
	"application/pdf": true,
	"text/plain":      true,
	"text/html":       true,
	"text/csv":        true,
	"text/markdown":   true,
}

// InboundResult holds the converted content from an A2A message.
type InboundResult struct {
	Text      string
	Images    []agent.ImageBlock
	Documents []agent.DocumentBlock
}

// ConvertInbound converts an A2A Message's parts into gude-agents content.
// TextParts are concatenated into Text. Raw and URL parts are converted
// to ImageBlock or DocumentBlock based on MIME type. Unrecognized MIME types
// are skipped with a warning log.
func ConvertInbound(msg *a2a.Message, logger *slog.Logger) InboundResult {
	var result InboundResult
	if msg == nil {
		return result
	}

	for _, part := range msg.Parts {
		if part == nil {
			continue
		}

		// Handle text parts — concatenate with newline separator.
		if t := part.Text(); t != "" {
			if result.Text != "" {
				result.Text += "\n"
			}
			result.Text += t
			continue
		}

		mime := part.MediaType

		// Handle Raw content (inline binary data).
		if raw := part.Raw(); raw != nil {
			if imageMIMETypes[mime] {
				encoded := base64.StdEncoding.EncodeToString(raw)
				result.Images = append(result.Images, agent.ImageBlock{
					Source: agent.ImageSource{
						Base64:   encoded,
						MIMEType: mime,
					},
				})
			} else if docMIMETypes[mime] {
				encoded := base64.StdEncoding.EncodeToString(raw)
				result.Documents = append(result.Documents, agent.DocumentBlock{
					Source: agent.DocumentSource{
						Base64:   encoded,
						MIMEType: mime,
					},
				})
			} else {
				logger.Warn("skipping part with unrecognized MIME type",
					"mimeType", mime,
					"contentType", "raw",
				)
			}
			continue
		}

		// Handle URL content (file reference).
		if u := part.URL(); u != "" {
			if imageMIMETypes[mime] {
				result.Images = append(result.Images, agent.ImageBlock{
					Source: agent.ImageSource{
						URL:      string(u),
						MIMEType: mime,
					},
				})
			} else if docMIMETypes[mime] {
				result.Documents = append(result.Documents, agent.DocumentBlock{
					Source: agent.DocumentSource{
						URL:      string(u),
						MIMEType: mime,
					},
				})
			} else {
				logger.Warn("skipping part with unrecognized MIME type",
					"mimeType", mime,
					"contentType", "url",
				)
			}
			continue
		}
	}

	return result
}

// ConvertOutboundImage converts an ImageBlock to an A2A Part.
// For Base64 sources, it emits a Raw part with the decoded bytes and MediaType.
// For URL sources, it emits a URL part.
// For raw Data sources, it emits a Raw part with the bytes and MediaType.
func ConvertOutboundImage(img agent.ImageBlock) *a2a.Part {
	src := img.Source

	// URL source → FilePart (URL content).
	if src.URL != "" {
		return &a2a.Part{
			Content:   a2a.URL(src.URL),
			MediaType: src.MIMEType,
		}
	}

	// Raw Data source → encode to base64, emit as Raw part.
	if len(src.Data) > 0 {
		p := a2a.NewRawPart(src.Data)
		p.MediaType = src.MIMEType
		return p
	}

	// Base64 source → decode and emit as Raw part.
	if src.Base64 != "" {
		decoded, _ := base64.StdEncoding.DecodeString(src.Base64)
		p := a2a.NewRawPart(decoded)
		p.MediaType = src.MIMEType
		return p
	}

	return nil
}

// ConvertOutboundDocument converts a DocumentBlock to an A2A Part.
// For Base64 sources, it emits a Raw part with the decoded bytes and MediaType.
// For URL sources, it emits a URL part.
// For raw Data sources, it emits a Raw part with the bytes and MediaType.
func ConvertOutboundDocument(doc agent.DocumentBlock) *a2a.Part {
	src := doc.Source

	// URL source → FilePart (URL content).
	if src.URL != "" {
		return &a2a.Part{
			Content:   a2a.URL(src.URL),
			MediaType: src.MIMEType,
		}
	}

	// Raw Data source → encode to base64, emit as Raw part.
	if len(src.Data) > 0 {
		p := a2a.NewRawPart(src.Data)
		p.MediaType = src.MIMEType
		return p
	}

	// Base64 source → decode and emit as Raw part.
	if src.Base64 != "" {
		decoded, _ := base64.StdEncoding.DecodeString(src.Base64)
		p := a2a.NewRawPart(decoded)
		p.MediaType = src.MIMEType
		return p
	}

	return nil
}
