package document

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// setupDepthTree creates a directory tree for depth tests:
//
//	root/
//	  a.txt          (depth 1)
//	  sub1/
//	    b.txt        (depth 2)
//	    sub2/
//	      c.txt      (depth 3)
func setupDepthTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	os.WriteFile(filepath.Join(root, "a.txt"), []byte("file a"), 0644)

	sub1 := filepath.Join(root, "sub1")
	os.MkdirAll(sub1, 0755)
	os.WriteFile(filepath.Join(sub1, "b.txt"), []byte("file b"), 0644)

	sub2 := filepath.Join(sub1, "sub2")
	os.MkdirAll(sub2, 0755)
	os.WriteFile(filepath.Join(sub2, "c.txt"), []byte("file c"), 0644)

	return root
}

func TestLoadDir_DefaultUnlimited(t *testing.T) {
	root := setupDepthTree(t)

	texts, _, err := LoadDir(context.Background(), root, WithExtensions(".txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 3 {
		t.Fatalf("expected 3 files (unlimited depth), got %d", len(texts))
	}
}

func TestLoadDir_MaxDepth1_FlatOnly(t *testing.T) {
	root := setupDepthTree(t)

	texts, meta, err := LoadDir(context.Background(), root, WithExtensions(".txt"), WithMaxDepth(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 {
		t.Fatalf("expected 1 file (depth 1 = flat), got %d", len(texts))
	}
	if filepath.Base(meta[0]["source"]) != "a.txt" {
		t.Errorf("expected a.txt, got %s", meta[0]["source"])
	}
}

func TestLoadDir_MaxDepth2_OneLevel(t *testing.T) {
	root := setupDepthTree(t)

	texts, _, err := LoadDir(context.Background(), root, WithExtensions(".txt"), WithMaxDepth(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 2 {
		t.Fatalf("expected 2 files (depth 2), got %d", len(texts))
	}
}

func TestLoadDir_MaxDepth3_AllFiles(t *testing.T) {
	root := setupDepthTree(t)

	texts, _, err := LoadDir(context.Background(), root, WithExtensions(".txt"), WithMaxDepth(3))
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 3 {
		t.Fatalf("expected 3 files (depth 3 covers all), got %d", len(texts))
	}
}
