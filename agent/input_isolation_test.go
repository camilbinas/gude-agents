package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWithImages_CopiesSliceAndData(t *testing.T) {
	data := []byte{0x1, 0x2, 0x3}
	imgs := []ImageBlock{{Source: ImageSource{Data: data, MIMEType: "image/png"}}}

	c := Background().WithImages(imgs)

	imgs[0].Source.Data[0] = 0xFF
	imgs[0] = ImageBlock{Source: ImageSource{Data: []byte{0x9}, MIMEType: "image/jpeg"}}

	got := c.Images()
	if len(got) != 1 {
		t.Fatalf("expected 1 image, got %d", len(got))
	}
	if got[0].Source.MIMEType != "image/png" {
		t.Fatalf("slice element aliased: MIMEType = %q, want image/png", got[0].Source.MIMEType)
	}
	if got[0].Source.Data[0] != 0x1 {
		t.Fatalf("Source.Data aliased: byte[0] = %#x, want 0x1", got[0].Source.Data[0])
	}
}

func TestWithDocuments_CopiesSliceAndData(t *testing.T) {
	data := []byte("hello")
	docs := []DocumentBlock{{Source: DocumentSource{Data: data, MIMEType: "text/plain"}}}

	c := Background().WithDocuments(docs)

	docs[0].Source.Data[0] = 'X'
	docs[0] = DocumentBlock{Source: DocumentSource{Data: []byte("other"), MIMEType: "text/html"}}

	got := c.Documents()
	if len(got) != 1 {
		t.Fatalf("expected 1 document, got %d", len(got))
	}
	if got[0].Source.MIMEType != "text/plain" {
		t.Fatalf("slice element aliased: MIMEType = %q, want text/plain", got[0].Source.MIMEType)
	}
	if string(got[0].Source.Data) != "hello" {
		t.Fatalf("Source.Data aliased: %q, want hello", string(got[0].Source.Data))
	}
}

func TestWithImages_NilStaysNil(t *testing.T) {
	if got := Background().WithImages(nil).Images(); got != nil {
		t.Fatalf("WithImages(nil).Images() = %v, want nil", got)
	}
	if got := Background().WithDocuments(nil).Documents(); got != nil {
		t.Fatalf("WithDocuments(nil).Documents() = %v, want nil", got)
	}
}

func TestPrincipal_CredentialsNotSerialized(t *testing.T) {
	p := Principal{
		ID:          "u1",
		Roles:       []string{"admin"},
		Attrs:       map[string]string{"org": "acme"},
		Credentials: map[string]string{"api_key": "SECRET-TOKEN"},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal principal: %v", err)
	}
	if strings.Contains(string(b), "SECRET-TOKEN") || strings.Contains(string(b), "api_key") {
		t.Fatalf("Principal JSON leaked credentials: %s", b)
	}
	if p.Credential("api_key") != "SECRET-TOKEN" {
		t.Fatal("Credential accessor should still return the value in-process")
	}
}

func TestAuditRecord_CredentialsNotSerialized(t *testing.T) {
	rec := AuditRecord{
		Event:     AuditEventToolCall,
		Principal: Principal{ID: "u1", Credentials: map[string]string{"token": "LEAK-ME"}},
		ToolName:  "search",
		Allowed:   true,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal audit record: %v", err)
	}
	if strings.Contains(string(b), "LEAK-ME") {
		t.Fatalf("AuditRecord JSON leaked credentials: %s", b)
	}
}

func TestInvokeAuditRecord_CredentialsNotSerialized(t *testing.T) {
	rec := InvokeAuditRecord{
		Event:     AuditEventInvokeStart,
		Principal: Principal{ID: "u1", Credentials: map[string]string{"token": "LEAK-ME"}},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal invoke audit record: %v", err)
	}
	if strings.Contains(string(b), "LEAK-ME") {
		t.Fatalf("InvokeAuditRecord JSON leaked credentials: %s", b)
	}
}

func TestInvokeRecord_CredentialsNotSerialized(t *testing.T) {
	rec := InvokeRecord{
		Principal: Principal{ID: "u1", Credentials: map[string]string{"token": "LEAK-ME"}},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal invoke record: %v", err)
	}
	if strings.Contains(string(b), "LEAK-ME") {
		t.Fatalf("InvokeRecord JSON leaked credentials: %s", b)
	}
}

func TestWithStopSequences_RejectsEmpty(t *testing.T) {
	_, err := New(plainProvider{}, "x", WithStopSequences([]string{"STOP", ""}))
	if err == nil {
		t.Fatal("expected error for empty stop sequence")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithStopSequences_CopiesSlice(t *testing.T) {
	seqs := []string{"STOP", "END"}
	a, err := New(plainProvider{}, "x", WithStopSequences(seqs))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	seqs[0] = "MUTATED"
	if got := a.inferenceConfig.StopSequences[0]; got != "STOP" {
		t.Fatalf("stop sequence aliased caller slice: got %q, want STOP", got)
	}
}

func TestValidateToolInput_EnumTypeAware(t *testing.T) {
	// A numeric enum must reject a string that formats the same.
	numSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"n": map[string]any{"enum": []any{float64(1), float64(2)}},
		},
	}
	if err := ValidateToolInput(numSchema, json.RawMessage(`{"n":1}`)); err != nil {
		t.Fatalf("numeric 1 should satisfy enum [1,2]: %v", err)
	}
	if err := ValidateToolInput(numSchema, json.RawMessage(`{"n":"1"}`)); err == nil {
		t.Fatal(`string "1" must NOT satisfy a numeric enum [1,2]`)
	}

	bigSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"n": map[string]any{"enum": []any{float64(1e10)}},
		},
	}
	if err := ValidateToolInput(bigSchema, json.RawMessage(`{"n":10000000000}`)); err != nil {
		t.Fatalf("1e10 should match 10000000000 by value: %v", err)
	}

	strSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"c": map[string]any{"enum": []any{"red", "green"}},
		},
	}
	if err := ValidateToolInput(strSchema, json.RawMessage(`{"c":"red"}`)); err != nil {
		t.Fatalf("red should satisfy string enum: %v", err)
	}
	if err := ValidateToolInput(strSchema, json.RawMessage(`{"c":"blue"}`)); err == nil {
		t.Fatal("blue must not satisfy string enum [red,green]")
	}

	boolSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"b": map[string]any{"enum": []any{true}},
		},
	}
	if err := ValidateToolInput(boolSchema, json.RawMessage(`{"b":true}`)); err != nil {
		t.Fatalf("true should satisfy bool enum: %v", err)
	}
	if err := ValidateToolInput(boolSchema, json.RawMessage(`{"b":"true"}`)); err == nil {
		t.Fatal(`string "true" must not satisfy a boolean enum`)
	}
}

func TestNew_SkipsNilOptions(t *testing.T) {
	a, err := New(plainProvider{}, "x", nil, WithName("named"), nil)
	if err != nil {
		t.Fatalf("New with nil options should succeed: %v", err)
	}
	if a.Name() != "named" {
		t.Fatalf("Name() = %q, want named", a.Name())
	}
}
