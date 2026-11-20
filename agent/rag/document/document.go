// Package document provides file-to-text extraction for RAG ingestion pipelines.
// It supports plain text, Markdown, CSV, JSON, and DOCX files out of the box.
// PDF support requires the agent/rag/document/pdf submodule.
//
// Usage:
//
//	texts, metadata, err := document.LoadFiles(ctx, "docs/guide.md", "data/faq.docx")
//	if err != nil { ... }
//	err = rag.Ingest(ctx, store, embedder, texts, metadata)
//
// Or load an entire directory:
//
//	texts, metadata, err := document.LoadDir(ctx, "docs/", document.WithExtensions(".md", ".txt"))
//	if err != nil { ... }
//	err = rag.Ingest(ctx, store, embedder, texts, metadata)
package document

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent/rag"
)

// Parser extracts text from a file. Implementations handle specific file formats.
type Parser interface {
	Parse(ctx context.Context, path string) (string, error)
}

// ParserFunc adapts a function to the Parser interface.
type ParserFunc func(ctx context.Context, path string) (string, error)

func (f ParserFunc) Parse(ctx context.Context, path string) (string, error) {
	return f(ctx, path)
}

// defaultParsersMu guards defaultParsers against a RegisterParser call racing a
// concurrent lookup.
var defaultParsersMu sync.RWMutex

// defaultParsers maps file extensions to their parsers. Access only through
// defaultParsersMu.
var defaultParsers = map[string]Parser{
	".txt":  ParserFunc(parseText),
	".md":   ParserFunc(parseText),
	".csv":  ParserFunc(parseText),
	".json": ParserFunc(parseText),
	".yaml": ParserFunc(parseText),
	".yml":  ParserFunc(parseText),
	".xml":  ParserFunc(parseText),
	".html": ParserFunc(parseText),
	".go":   ParserFunc(parseText),
	".py":   ParserFunc(parseText),
	".js":   ParserFunc(parseText),
	".ts":   ParserFunc(parseText),
	".docx": ParserFunc(parseDocx),
}

// RegisterParser adds or overrides a parser for a file extension. The extension
// should include the dot (e.g. ".pdf"). Safe for concurrent use.
func RegisterParser(ext string, p Parser) {
	defaultParsersMu.Lock()
	defer defaultParsersMu.Unlock()
	defaultParsers[strings.ToLower(ext)] = p
}

// LoadOption configures LoadFiles and LoadDir.
type LoadOption func(*loadConfig)

type loadConfig struct {
	extensions []string // if set, only load files with these extensions
	parsers    map[string]Parser
	maxDepth   int // 0 = unlimited (default); 1 = flat (no subdirectories); n = n levels deep
	maxBytes   int64
}

// DefaultMaxFileBytes is the default cap on the size of a loaded file and on
// the decompressed size of a DOCX body.
const DefaultMaxFileBytes int64 = 50 << 20

type limitKey struct{}

// fileLimit returns the byte cap carried by ctx, or the default.
func fileLimit(ctx context.Context) int64 {
	if n, ok := ctx.Value(limitKey{}).(int64); ok && n > 0 {
		return n
	}
	return DefaultMaxFileBytes
}

// WithMaxFileBytes caps the size of each loaded file (and the decompressed
// size of DOCX content). Larger files make LoadFiles fail and are skipped by
// LoadDir. n <= 0 keeps the default of DefaultMaxFileBytes (50MiB).
func WithMaxFileBytes(n int64) LoadOption {
	return func(c *loadConfig) {
		if n > 0 {
			c.maxBytes = n
		}
	}
}

// WithExtensions filters files to only those with the given extensions.
// Extensions should include the dot (e.g. ".md", ".txt").
func WithExtensions(exts ...string) LoadOption {
	return func(c *loadConfig) {
		for _, ext := range exts {
			c.extensions = append(c.extensions, strings.ToLower(ext))
		}
	}
}

// WithParser adds a custom parser for a specific extension, scoped to this call.
func WithParser(ext string, p Parser) LoadOption {
	return func(c *loadConfig) {
		c.parsers[strings.ToLower(ext)] = p
	}
}

// WithMaxDepth limits how deep LoadDir descends into subdirectories.
// A depth of 1 means only files directly in the target directory (flat).
// A depth of 2 includes one level of subdirectories, and so on.
// The default (0) means unlimited depth — all subdirectories are walked.
func WithMaxDepth(depth int) LoadOption {
	return func(c *loadConfig) {
		c.maxDepth = depth
	}
}

func newLoadConfig(opts []LoadOption) *loadConfig {
	cfg := &loadConfig{parsers: make(map[string]Parser), maxBytes: DefaultMaxFileBytes}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

func (c *loadConfig) parserFor(ext string) (Parser, bool) {
	ext = strings.ToLower(ext)
	if p, ok := c.parsers[ext]; ok {
		return p, true
	}
	defaultParsersMu.RLock()
	defer defaultParsersMu.RUnlock()
	p, ok := defaultParsers[ext]
	return p, ok
}

func (c *loadConfig) allowExt(ext string) bool {
	if len(c.extensions) == 0 {
		return true
	}
	ext = strings.ToLower(ext)
	for _, e := range c.extensions {
		if e == ext {
			return true
		}
	}
	return false
}

// LoadFiles reads and extracts text from the given file paths.
// Returns parallel slices of text content and metadata (one per file).
// Extracted text is normalized: consecutive whitespace is collapsed and lines are trimmed.
// Unsupported file extensions return an error.
func LoadFiles(ctx context.Context, paths []string, opts ...LoadOption) (texts []string, metadata []map[string]string, err error) {
	cfg := newLoadConfig(opts)

	for _, path := range paths {
		ext := filepath.Ext(path)
		parser, ok := cfg.parserFor(ext)
		if !ok {
			return nil, nil, fmt.Errorf("document: unsupported file type %q for %s (register a parser with RegisterParser)", ext, path)
		}

		fi, err := os.Stat(path)
		if err != nil {
			return nil, nil, fmt.Errorf("document: stat %s: %w", path, err)
		}
		if fi.Size() > cfg.maxBytes {
			return nil, nil, fmt.Errorf("document: %s is %d bytes, exceeds limit of %d (see WithMaxFileBytes)", path, fi.Size(), cfg.maxBytes)
		}

		text, err := parser.Parse(context.WithValue(ctx, limitKey{}, cfg.maxBytes), path)
		if err != nil {
			return nil, nil, fmt.Errorf("document: parse %s: %w", path, err)
		}

		text = normalizeWhitespace(text)

		texts = append(texts, text)
		metadata = append(metadata, map[string]string{
			"source":    path,
			"filename":  filepath.Base(path),
			"extension": ext,
		})
	}

	return texts, metadata, nil
}

// LoadDir walks a directory and extracts text from all supported files.
// Symbolic links (to files or directories) are not followed and non-regular
// files are skipped, so a link inside the tree cannot pull in content from
// outside it. Files larger than the WithMaxFileBytes limit are skipped.
// Use WithExtensions to filter by file type and WithMaxDepth to limit recursion.
func LoadDir(ctx context.Context, dir string, opts ...LoadOption) (texts []string, metadata []map[string]string, err error) {
	cfg := newLoadConfig(opts)

	// Clean the root so depth calculation is consistent.
	dir = filepath.Clean(dir)
	rootDepth := strings.Count(dir, string(filepath.Separator))

	var paths []string
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip subdirectories beyond maxDepth.
			if cfg.maxDepth > 0 {
				pathDepth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
				if pathDepth >= cfg.maxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		// Only regular files: skips symlinks (Walk uses Lstat), devices, FIFOs.
		if !info.Mode().IsRegular() || info.Size() > cfg.maxBytes {
			return nil
		}
		// Check file depth (file's parent depth must be within limit).
		if cfg.maxDepth > 0 {
			fileDepth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if fileDepth > cfg.maxDepth {
				return nil
			}
		}
		ext := filepath.Ext(path)
		if !cfg.allowExt(ext) {
			return nil
		}
		if _, ok := cfg.parserFor(ext); !ok {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("document: walk %s: %w", dir, walkErr)
	}

	return LoadFiles(ctx, paths, opts...)
}

// parseText reads a file as plain text.
func parseText(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	limit := fileLimit(ctx)
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit {
		return "", fmt.Errorf("file exceeds limit of %d bytes", limit)
	}
	return string(data), nil
}

// reMultiSpace matches two or more consecutive spaces/tabs on the same line.
var reMultiSpace = regexp.MustCompile(`[^\S\n]{2,}`)

// reMultiNewline matches three or more consecutive newlines (preserves paragraph breaks).
var reMultiNewline = regexp.MustCompile(`\n{3,}`)

// normalizeWhitespace collapses excessive whitespace from extracted text:
//   - Multiple spaces/tabs on a line → single space
//   - Single newlines → space (visual line breaks aren't semantic)
//   - Double newlines preserved (paragraph breaks)
//   - 3+ consecutive newlines → double newline
//   - Leading/trailing whitespace removed
func normalizeWhitespace(s string) string {
	// Collapse inline whitespace.
	s = reMultiSpace.ReplaceAllString(s, " ")

	// Collapse 3+ newlines to double newline.
	s = reMultiNewline.ReplaceAllString(s, "\n\n")

	// Replace single newlines with spaces, preserving double newlines (paragraph breaks).
	// Split on paragraph breaks, collapse newlines within each paragraph, rejoin.
	paragraphs := strings.Split(s, "\n\n")
	for i, p := range paragraphs {
		paragraphs[i] = strings.ReplaceAll(strings.TrimSpace(p), "\n", " ")
	}
	s = strings.Join(paragraphs, "\n\n")

	// Clean up any double spaces introduced.
	s = reMultiSpace.ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}

// parseDocx extracts text from a .docx file by reading the XML content.
func parseDocx(ctx context.Context, path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			limit := fileLimit(ctx)
			if f.UncompressedSize64 > uint64(limit) {
				return "", fmt.Errorf("document.xml decompresses to %d bytes, exceeds limit of %d", f.UncompressedSize64, limit)
			}
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("open document.xml: %w", err)
			}
			defer rc.Close()
			// The declared size can lie; enforce the cap while reading.
			lr := &io.LimitedReader{R: rc, N: limit + 1}
			text, err := extractDocxText(lr)
			if lr.N <= 0 {
				return "", fmt.Errorf("document.xml exceeds limit of %d bytes", limit)
			}
			return text, err
		}
	}

	return "", fmt.Errorf("document.xml not found in docx")
}

// extractDocxText parses the XML from word/document.xml and extracts text runs.
func extractDocxText(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var sb strings.Builder
	var inParagraph bool
	var paragraphHasText bool

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode docx xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			// w:p = paragraph, w:t = text run
			if t.Name.Local == "p" {
				if paragraphHasText {
					sb.WriteString("\n")
				}
				inParagraph = true
				paragraphHasText = false
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				inParagraph = false
			}
		case xml.CharData:
			if inParagraph {
				text := strings.TrimSpace(string(t))
				if text != "" {
					if paragraphHasText {
						sb.WriteString(" ")
					}
					sb.WriteString(text)
					paragraphHasText = true
				}
			}
		}
	}

	return sb.String(), nil
}

// IngestFiles loads text from the given file paths and ingests them into the
// vector store using the provided embedder. It combines LoadFiles and rag.Ingest
// into a single call.
//
//	err := document.IngestFiles(ctx, store, embedder, []string{"guide.pdf", "faq.md"})
func IngestFiles(
	ctx context.Context,
	store rag.Store,
	embedder rag.Embedder,
	paths []string,
	loadOpts []LoadOption,
	ingestOpts ...rag.IngestOption,
) error {
	texts, metadata, err := LoadFiles(ctx, paths, loadOpts...)
	if err != nil {
		return err
	}
	return rag.Ingest(ctx, store, embedder, texts, metadata, ingestOpts...)
}

// IngestDir walks a directory, loads all supported files, and ingests them into
// the vector store using the provided embedder. It combines LoadDir and rag.Ingest
// into a single call.
//
//	err := document.IngestDir(ctx, store, embedder, "docs/",
//	    []document.LoadOption{document.WithExtensions(".pdf", ".md")},
//	    rag.WithConcurrency(10),
//	)
func IngestDir(
	ctx context.Context,
	store rag.Store,
	embedder rag.Embedder,
	dir string,
	loadOpts []LoadOption,
	ingestOpts ...rag.IngestOption,
) error {
	texts, metadata, err := LoadDir(ctx, dir, loadOpts...)
	if err != nil {
		return err
	}
	return rag.Ingest(ctx, store, embedder, texts, metadata, ingestOpts...)
}
