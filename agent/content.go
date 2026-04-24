package agent

import (
	"fmt"
	"strings"
)

// ImageSource holds image data as either raw bytes, a pre-encoded base64 string,
// or a URL pointing to a hosted image, plus the MIME type that identifies the
// image format.
// Set exactly one of Data, Base64, or URL.
type ImageSource struct {
	Data     []byte // raw image bytes; mutually exclusive with Base64 and URL
	Base64   string // pre-encoded base64 string (RFC 4648); mutually exclusive with Data and URL
	URL      string // publicly accessible image URL; mutually exclusive with Data and Base64
	MIMEType string // must be one of the four supported MIME types (required for Data/Base64, optional for URL)
}

// validMIMETypes is the set of MIME types accepted by all four providers.
var validMIMETypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// Validate returns nil if the ImageSource is well-formed.
// For Data/Base64 sources, MIMEType must be one of the four supported values.
// For URL sources, MIMEType validation is skipped (the provider resolves it).
func (s ImageSource) Validate() error {
	if s.URL != "" {
		return nil
	}
	if !validMIMETypes[s.MIMEType] {
		return fmt.Errorf("unsupported image MIME type %s: must be one of image/jpeg, image/png, image/gif, image/webp", s.MIMEType)
	}
	return nil
}

// ImageMIMEFromExt returns the image MIME type for a file extension.
// The extension should include the dot (e.g. ".jpg", ".png").
// Returns an error for unsupported extensions.
func ImageMIMEFromExt(ext string) (string, error) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".png":
		return "image/png", nil
	case ".gif":
		return "image/gif", nil
	case ".webp":
		return "image/webp", nil
	default:
		return "", fmt.Errorf("unsupported image extension %q: use .jpg, .png, .gif, or .webp", ext)
	}
}

// ImageBlock is a ContentBlock that carries image data.
// Documented in docs/message-types.md — update when changing fields.
type ImageBlock struct {
	Source ImageSource
}

func (ImageBlock) contentBlock() {}

// DocumentSource holds document data as either raw bytes, a pre-encoded base64
// string, or a URL pointing to a hosted document, plus the MIME type.
// Set exactly one of Data, Base64, or URL.
type DocumentSource struct {
	Data     []byte // raw document bytes; mutually exclusive with Base64 and URL
	Base64   string // pre-encoded base64 string (RFC 4648); mutually exclusive with Data and URL
	URL      string // publicly accessible document URL; mutually exclusive with Data and Base64
	MIMEType string // e.g. "application/pdf", "text/plain", "text/html", "text/csv", "text/markdown"
	Name     string // optional filename hint for the provider
}

// validDocMIMETypes is the set of document MIME types accepted by providers.
var validDocMIMETypes = map[string]bool{
	"application/pdf": true,
	"text/plain":      true,
	"text/html":       true,
	"text/csv":        true,
	"text/markdown":   true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true, // .docx
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       true, // .xlsx
	"application/msword":       true, // .doc
	"application/vnd.ms-excel": true, // .xls
}

// Validate returns nil if the DocumentSource is well-formed.
// For Data/Base64 sources, MIMEType must be a supported document type.
// For URL sources, MIMEType validation is skipped.
func (s DocumentSource) Validate() error {
	if s.URL != "" {
		return nil
	}
	if !validDocMIMETypes[s.MIMEType] {
		return fmt.Errorf("unsupported document MIME type %s", s.MIMEType)
	}
	return nil
}

// DocumentMIMEFromExt returns the document MIME type for a file extension.
// The extension should include the dot (e.g. ".pdf", ".docx").
// Returns an error for unsupported extensions.
func DocumentMIMEFromExt(ext string) (string, error) {
	switch strings.ToLower(ext) {
	case ".pdf":
		return "application/pdf", nil
	case ".txt":
		return "text/plain", nil
	case ".html", ".htm":
		return "text/html", nil
	case ".csv":
		return "text/csv", nil
	case ".md":
		return "text/markdown", nil
	case ".doc":
		return "application/msword", nil
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil
	case ".xls":
		return "application/vnd.ms-excel", nil
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil
	default:
		return "", fmt.Errorf("unsupported document extension %q", ext)
	}
}

// DocumentBlock is a ContentBlock that carries an inline document (PDF, etc.).
// Documented in docs/message-types.md — update when changing fields.
type DocumentBlock struct {
	Source DocumentSource
}

func (DocumentBlock) contentBlock() {}
