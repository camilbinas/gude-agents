package disk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "disk-memory")
	return dir
}

func saveLatest(ctx context.Context, store agent.ConversationStore, id string, messages []agent.Message) error {
	snapshot, err := store.Load(ctx, id)
	if err != nil {
		return err
	}
	_, err = store.Save(ctx, id, messages, snapshot.Revision)
	return err
}

func loadMessages(ctx context.Context, store agent.ConversationStore, id string) ([]agent.Message, error) {
	snapshot, err := store.Load(ctx, id)
	return snapshot.Messages, err
}

func TestNew_CreatesDirectory(t *testing.T) {
	dir := tempDir(t)
	m, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil DiskMemory")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestNew_EmptyDir(t *testing.T) {
	_, err := New("")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestSaveAndLoad(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi there"}}},
	}

	if err := saveLatest(ctx, m, "conv-1", msgs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := loadMessages(ctx, m, "conv-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if tb, ok := loaded[0].Content[0].(agent.TextBlock); !ok || tb.Text != "hello" {
		t.Errorf("expected 'hello', got %v", loaded[0].Content[0])
	}
	if tb, ok := loaded[1].Content[0].(agent.TextBlock); !ok || tb.Text != "hi there" {
		t.Errorf("expected 'hi there', got %v", loaded[1].Content[0])
	}
}

func TestLoad_NotFound(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := loadMessages(context.Background(), m, "nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty slice, got %d messages", len(msgs))
	}
}

func TestList(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	msg := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "x"}}},
	}

	saveLatest(ctx, m, "alpha", msg)
	saveLatest(ctx, m, "beta", msg)
	saveLatest(ctx, m, "gamma", msg)

	ids, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 conversations, got %d: %v", len(ids), ids)
	}
}

func TestDelete(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	msg := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "x"}}},
	}

	saveLatest(ctx, m, "to-delete", msg)

	// Verify it exists.
	loaded, _ := loadMessages(ctx, m, "to-delete")
	if len(loaded) == 0 {
		t.Fatal("expected message before delete")
	}

	// Delete.
	if err := m.Delete(ctx, "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it's gone.
	loaded, _ = loadMessages(ctx, m, "to-delete")
	if len(loaded) != 0 {
		t.Errorf("expected empty after delete, got %d", len(loaded))
	}
}

func TestDelete_NotFound(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}

	// Deleting a nonexistent conversation should not error.
	if err := m.Delete(context.Background(), "ghost"); err != nil {
		t.Fatalf("Delete nonexistent: %v", err)
	}
}

func TestSave_Overwrite(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	msgs1 := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "first"}}},
	}
	msgs2 := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "second"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "reply"}}},
	}

	saveLatest(ctx, m, "conv", msgs1)
	saveLatest(ctx, m, "conv", msgs2)

	loaded, _ := loadMessages(ctx, m, "conv")
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages after overwrite, got %d", len(loaded))
	}
	if tb, ok := loaded[0].Content[0].(agent.TextBlock); !ok || tb.Text != "second" {
		t.Errorf("expected 'second', got %v", loaded[0].Content[0])
	}
}

func TestPathSanitization(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Conversation IDs with path traversal characters should be sanitized.
	msg := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "safe"}}},
	}

	if err := saveLatest(ctx, m, "../../../etc/passwd", msg); err != nil {
		t.Fatalf("Save with traversal ID: %v", err)
	}

	// Should be stored safely in the configured directory, not at /etc/passwd.
	loaded, err := loadMessages(ctx, m, "../../../etc/passwd")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded))
	}
}

func TestToolBlocks(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
			agent.TextBlock{Text: "Let me look that up."},
			agent.ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: []byte(`{"q":"test"}`)},
		}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.ToolResultBlock{ToolUseID: "tu-1", Content: "found it", IsError: false},
		}},
	}

	saveLatest(ctx, m, "tools", msgs)
	loaded, _ := loadMessages(ctx, m, "tools")

	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}

	// Check tool use block.
	tu, ok := loaded[0].Content[1].(agent.ToolUseBlock)
	if !ok {
		t.Fatalf("expected ToolUseBlock, got %T", loaded[0].Content[1])
	}
	if tu.Name != "search" {
		t.Errorf("expected tool name 'search', got %q", tu.Name)
	}

	// Check tool result block.
	tr, ok := loaded[1].Content[0].(agent.ToolResultBlock)
	if !ok {
		t.Fatalf("expected ToolResultBlock, got %T", loaded[1].Content[0])
	}
	if tr.Content != "found it" {
		t.Errorf("expected 'found it', got %q", tr.Content)
	}
}

// TestImageBlock verifies that disk memory correctly round-trips an ImageBlock
// through Save + Load — both raw bytes and pre-encoded base64 representations.
// This is the end-to-end memory persistence test for the image-input feature.
func TestImageBlock(t *testing.T) {
	m, err := New(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	rawBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	const preEncoded = "aGVsbG8gaW1hZ2UgZGF0YQ=="

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.ImageBlock{Source: agent.ImageSource{Data: rawBytes, MIMEType: "image/jpeg"}},
			agent.ImageBlock{Source: agent.ImageSource{Base64: preEncoded, MIMEType: "image/png"}},
			agent.TextBlock{Text: "describe these"},
		}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
			agent.TextBlock{Text: "Two images, got it."},
		}},
	}

	if err := saveLatest(ctx, m, "images", msgs); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := loadMessages(ctx, m, "images")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if len(loaded[0].Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(loaded[0].Content))
	}

	// First block: raw bytes image.
	img0, ok := loaded[0].Content[0].(agent.ImageBlock)
	if !ok {
		t.Fatalf("content[0]: expected ImageBlock, got %T", loaded[0].Content[0])
	}
	if string(img0.Source.Data) != string(rawBytes) {
		t.Errorf("raw bytes corrupted: expected %v, got %v", rawBytes, img0.Source.Data)
	}
	if img0.Source.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType: expected 'image/jpeg', got %q", img0.Source.MIMEType)
	}

	// Second block: pre-encoded base64 image.
	img1, ok := loaded[0].Content[1].(agent.ImageBlock)
	if !ok {
		t.Fatalf("content[1]: expected ImageBlock, got %T", loaded[0].Content[1])
	}
	if img1.Source.Base64 != preEncoded {
		t.Errorf("base64 corrupted: expected %q, got %q", preEncoded, img1.Source.Base64)
	}
	if img1.Source.MIMEType != "image/png" {
		t.Errorf("MIMEType: expected 'image/png', got %q", img1.Source.MIMEType)
	}

	// Third block: text survives alongside images.
	tb, ok := loaded[0].Content[2].(agent.TextBlock)
	if !ok {
		t.Fatalf("content[2]: expected TextBlock, got %T", loaded[0].Content[2])
	}
	if tb.Text != "describe these" {
		t.Errorf("text corrupted: expected %q, got %q", "describe these", tb.Text)
	}
}

func TestCASConcurrentStoresOneWinner(t *testing.T) {
	dir := tempDir(t)
	first, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, store := range []*Conversation{first, second} {
		wg.Add(1)
		go func(i int, store *Conversation) {
			defer wg.Done()
			<-start
			_, err := store.Save(ctx, "conv", []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: string(rune('a' + i))}}}}, 0)
			errs <- err
		}(i, store)
	}
	close(start)
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, agent.ErrConversationConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}
}

func TestLegacyRawMessageFileUpgradesWithCAS(t *testing.T) {
	dir := tempDir(t)
	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`[{"role":"user","content":[{"type":"text","text":"legacy"}]}]`)
	if err := os.WriteFile(m.path("legacy"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := m.Load(context.Background(), "legacy")
	if err != nil || snapshot.Revision != 0 || len(snapshot.Messages) != 1 {
		t.Fatalf("legacy snapshot = %+v, %v", snapshot, err)
	}
	if _, err := m.Save(context.Background(), "legacy", snapshot.Messages, snapshot.Revision); err != nil {
		t.Fatalf("upgrade Save: %v", err)
	}
	upgraded, err := m.Load(context.Background(), "legacy")
	if err != nil || upgraded.Revision != 1 || len(upgraded.Messages) != 1 {
		t.Fatalf("upgraded snapshot = %+v, %v", upgraded, err)
	}
}
