// Package markdown provides a markdown formatter for the webfetch tool.
//
// It converts HTML to clean markdown using html-to-markdown, producing
// structured output that LLMs can parse more efficiently than plain text.
// Headings, links, lists, code blocks, and tables are preserved.
//
// Usage:
//
//	import (
//		"github.com/camilbinas/gude-agents/agent/tool/webfetch"
//		"github.com/camilbinas/gude-agents/agent/tool/webfetch/markdown"
//	)
//
//	fetchTool := webfetch.New(webfetch.WithFormatter(markdown.Formatter()))
package markdown

import (
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/camilbinas/gude-agents/agent/tool/webfetch"
)

// Formatter returns a webfetch.Formatter that converts HTML to markdown.
func Formatter() webfetch.Formatter {
	return func(html string) (string, error) {
		return htmltomarkdown.ConvertString(html)
	}
}
