package agent

import (
	"encoding/json"
	"testing"

	"pgregory.net/rapid"
)

// TestProperty_WidgetBlockValidateRejectsEmptyType verifies that Validate()
// returns non-nil for any WidgetBlock with an empty Type, and nil for any
// non-empty Type.
func TestProperty_WidgetBlockValidateRejectsEmptyType(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an arbitrary payload (nil or some JSON bytes).
		useNilPayload := rapid.Bool().Draw(t, "useNilPayload")
		var payload json.RawMessage
		if !useNilPayload {
			raw := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "payloadBytes")
			payload = json.RawMessage(raw)
		}

		// Property 1a: empty Type must be rejected.
		emptyBlock := WidgetBlock{Type: "", Payload: payload}
		if err := emptyBlock.Validate(); err == nil {
			t.Fatalf("Validate() returned nil for WidgetBlock with empty Type; want non-nil error")
		}

		// Property 1b: any non-empty Type must be accepted.
		nonEmptyType := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`).Draw(t, "widgetType")
		validBlock := WidgetBlock{Type: nonEmptyType, Payload: payload}
		if err := validBlock.Validate(); err != nil {
			t.Fatalf("Validate() returned non-nil error %v for WidgetBlock with non-empty Type %q; want nil",
				err, nonEmptyType)
		}
	})
}

func TestDocumentSourceValidateProviderReferences(t *testing.T) {
	tests := []struct {
		name    string
		source  DocumentSource
		wantErr string
	}{
		{
			name:   "provider file ID",
			source: DocumentSource{FileID: "file_abc123"},
		},
		{
			name:   "Bedrock S3 URI with bucket owner",
			source: DocumentSource{S3URI: "s3://example-bucket/reports/quarterly.pdf", S3BucketOwner: "123456789012"},
		},
		{
			name:    "missing source",
			source:  DocumentSource{S3BucketOwner: "123456789012"},
			wantErr: "document source: one of Data, Base64, URL, FileID, or S3URI must be set",
		},
		{
			name:    "multiple provider sources",
			source:  DocumentSource{FileID: "file_abc123", S3URI: "s3://example-bucket/report.pdf"},
			wantErr: "document source: only one of Data, Base64, URL, FileID, or S3URI may be set",
		},
		{
			name:    "invalid S3 scheme",
			source:  DocumentSource{S3URI: "https://example-bucket.s3.amazonaws.com/report.pdf"},
			wantErr: "document source: S3URI must start with s3://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.source.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() returned unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
