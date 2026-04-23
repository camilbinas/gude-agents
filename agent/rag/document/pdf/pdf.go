// Package pdf provides a PDF text extractor for the document package.
// It uses ledongthuc/pdf for pure-Go PDF parsing with row-based extraction
// for better word boundary detection.
//
// Usage:
//
//	import (
//	    "github.com/camilbinas/gude-agents/agent/rag/document"
//	    _ "github.com/camilbinas/gude-agents/agent/rag/document/pdf" // registers .pdf parser
//	)
//
// The blank import registers the PDF parser automatically via init().
// After importing, LoadFiles and LoadDir will handle .pdf files.
package pdf

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/camilbinas/gude-agents/agent/rag/document"
	lpdf "github.com/ledongthuc/pdf"
)

func init() {
	document.RegisterParser(".pdf", document.ParserFunc(parsePDF))
}

func parsePDF(_ context.Context, path string) (string, error) {
	f, r, err := lpdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	totalPages := r.NumPage()

	for i := 1; i <= totalPages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}

		pageText := extractPageText(page)
		if pageText != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(pageText)
		}
	}

	return sb.String(), nil
}

// extractPageText uses GetTextByRow for positional text extraction.
// It uses X-position gaps between text elements to decide where to insert spaces,
// handling both word-level and character-level PDFs correctly.
// Falls back to GetPlainText if row extraction fails.
func extractPageText(page lpdf.Page) string {
	rows, err := page.GetTextByRow()
	if err != nil || len(rows) == 0 {
		text, err := page.GetPlainText(nil)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(text)
	}

	var sb strings.Builder
	for i, row := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		rowText := joinRowContent(row.Content)
		sb.WriteString(rowText)
	}

	return strings.TrimSpace(sb.String())
}

// joinRowContent joins text elements in a row using positional gaps to determine spacing.
// If the gap between two consecutive elements is large relative to the average character
// width, a space is inserted. Otherwise they're concatenated directly.
func joinRowContent(texts []lpdf.Text) string {
	if len(texts) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(texts[0].S)

	for j := 1; j < len(texts); j++ {
		prev := texts[j-1]
		curr := texts[j]

		// Estimate where the previous element ends.
		prevEnd := prev.X + prev.W
		gap := curr.X - prevEnd

		// Estimate average character width from the previous element.
		avgCharWidth := charWidth(prev)

		// If the gap is more than ~30% of a character width, insert a space.
		// This threshold works for both character-level PDFs (tiny gaps between
		// letters in the same word) and word-level PDFs (larger gaps between words).
		if gap > avgCharWidth*0.3 {
			sb.WriteByte(' ')
		} else if gap < -avgCharWidth*0.5 {
			// Negative gap (overlap) usually means a new line or repositioned text.
			sb.WriteByte(' ')
		}

		sb.WriteString(curr.S)
	}

	return sb.String()
}

// charWidth estimates the average character width of a text element.
func charWidth(t lpdf.Text) float64 {
	n := float64(len([]rune(t.S)))
	if n == 0 || t.W == 0 {
		return math.Max(t.FontSize*0.5, 1.0) // fallback estimate
	}
	return t.W / n
}
