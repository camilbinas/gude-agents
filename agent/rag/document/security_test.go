package document

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDir_SkipsSymlinks(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	os.WriteFile(secret, []byte("TOPSECRET"), 0o600)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "ok.txt"), []byte("fine"), 0o600)
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks unsupported")
	}
	os.Symlink(outside, filepath.Join(root, "linkdir"))

	texts, _, err := LoadDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 || texts[0] != "fine" {
		t.Fatalf("texts=%v", texts)
	}
}

func TestLoadFiles_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	os.WriteFile(p, []byte(strings.Repeat("a", 100)), 0o600)
	if _, _, err := LoadFiles(context.Background(), []string{p}, WithMaxFileBytes(50)); err == nil {
		t.Fatal("expected size error")
	}
	if _, _, err := LoadFiles(context.Background(), []string{p}, WithMaxFileBytes(500)); err != nil {
		t.Fatal(err)
	}
	// LoadDir skips oversized files silently.
	texts, _, err := LoadDir(context.Background(), dir, WithMaxFileBytes(50))
	if err != nil || len(texts) != 0 {
		t.Fatalf("texts=%v err=%v", texts, err)
	}
}

func TestParseText_LimitEnforcedWhileReading(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(p, []byte(strings.Repeat("a", 100)), 0o600)
	ctx := context.WithValue(context.Background(), limitKey{}, int64(10))
	if _, err := parseText(ctx, p); err == nil {
		t.Fatal("expected limit error")
	}
}

func TestParseDocx_ZipBomb(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bomb.docx")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte(`<w:document xmlns:w="x"><w:p><w:t>`))
	w.Write([]byte(strings.Repeat("A", 2<<20)))
	w.Write([]byte(`</w:t></w:p></w:document>`))
	zw.Close()
	f.Close()

	// Compressed file is tiny; decompressed exceeds a 1MiB cap.
	if _, _, err := LoadFiles(context.Background(), []string{p}, WithMaxFileBytes(1<<20)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err=%v", err)
	}
	if _, _, err := LoadFiles(context.Background(), []string{p}); err != nil {
		t.Fatalf("default limit should allow: %v", err)
	}
}
