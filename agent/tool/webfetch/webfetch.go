// Package webfetch provides a production-ready web fetch tool for agents.
//
// The tool fetches a URL and returns its text content, with sensible
// defaults for timeout, body size limits, redirect limits, and
// content-type filtering. It strips HTML tags and collapses whitespace
// to produce clean text suitable for LLM consumption.
//
// Usage:
//
//	fetchTool := webfetch.New()                            // defaults
//	fetchTool := webfetch.New(webfetch.WithMaxBytes(8192)) // custom

package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// defaults.
const (
	defaultTimeout      = 15 * time.Second
	defaultMaxBytes     = 32 * 1024 // 32 KB
	defaultMaxRedirects = 3
	defaultMaxChars     = 4000
)

// Option configures the fetch tool.
type Option func(*config)

// Formatter transforms raw HTML into a text representation for the LLM.
// The default formatter strips HTML tags and collapses whitespace.
// See the webfetch/markdown sub-package for a markdown formatter.
type Formatter func(html string) (string, error)

type config struct {
	timeout      time.Duration
	maxBytes     int64
	maxRedirects int
	maxChars     int
	client       *http.Client
	formatter    Formatter
}

// WithTimeout sets the HTTP request timeout. Default: 15s.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithMaxBytes sets the maximum response body size in bytes. Default: 32KB.
func WithMaxBytes(n int64) Option {
	return func(c *config) { c.maxBytes = n }
}

// WithMaxRedirects sets the maximum number of redirects to follow. Default: 5.
func WithMaxRedirects(n int) Option {
	return func(c *config) { c.maxRedirects = n }
}

// WithMaxChars sets the maximum number of characters in the returned text.
// Content beyond this limit is truncated with a "[truncated]" marker.
// Default: 4000.
func WithMaxChars(n int) Option {
	return func(c *config) { c.maxChars = n }
}

// WithClient sets a custom HTTP client. When set, timeout and redirect
// options are ignored — the caller is responsible for configuring them
// on the provided client.
func WithClient(client *http.Client) Option {
	return func(c *config) { c.client = client }
}

// WithFormatter sets a custom HTML-to-text formatter. The default strips
// HTML tags and collapses whitespace. Use the webfetch/markdown sub-package
// for markdown output:
//
//	webfetch.New(webfetch.WithFormatter(markdown.Formatter()))
func WithFormatter(f Formatter) Option {
	return func(c *config) { c.formatter = f }
}

// New creates a web_fetch tool with the given options.
func New(opts ...Option) tool.Tool {
	cfg := &config{
		timeout:      defaultTimeout,
		maxBytes:     defaultMaxBytes,
		maxRedirects: defaultMaxRedirects,
		maxChars:     defaultMaxChars,
	}
	for _, o := range opts {
		o(cfg)
	}

	client := cfg.client
	if client == nil {
		maxRedirects := cfg.maxRedirects
		client = &http.Client{
			Timeout: cfg.timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return fmt.Errorf("stopped after %d redirects", maxRedirects)
				}
				return nil
			},
		}
	}

	maxBytes := cfg.maxBytes
	maxChars := cfg.maxChars
	format := cfg.formatter

	if format == nil {
		format = defaultFormatter
	}

	return tool.NewRaw(
		"web_fetch",
		"Fetch a web page and return its text content. "+
			"Use after a web search to read a specific result in detail.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch",
				},
			},
			"required": []any{"url"},
		},
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var req struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return "", err
			}
			return fetchPage(ctx, client, req.URL, maxBytes, maxChars, format)
		},
	)
}

func fetchPage(ctx context.Context, client *http.Client, pageURL string, maxBytes int64, maxChars int, format Formatter) (string, error) {
	if pageURL == "" {
		return "", errors.New("url is required")
	}

	// Block non-HTTP(S) schemes.
	if !strings.HasPrefix(pageURL, "http://") && !strings.HasPrefix(pageURL, "https://") {
		return "", fmt.Errorf("unsupported scheme in URL %q: only http and https are allowed", pageURL)
	}

	// Block private/loopback IPs to prevent SSRF.
	if isPrivateURL(pageURL) {
		return "", fmt.Errorf("URL %q resolves to a private or loopback address", pageURL)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "gude-agents/fetch-tool")
	req.Header.Set("Accept", "text/html, text/plain, application/json, application/xml, */*;q=0.1")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch returned HTTP %d", resp.StatusCode)
	}

	// Only allow text-based content types.
	ct := resp.Header.Get("Content-Type")
	if !isTextContent(ct) {
		return "", fmt.Errorf("non-text content type: %s", ct)
	}

	// Read with size limit.
	limited := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	truncatedBySize := false
	if int64(len(body)) > maxBytes {
		body = body[:maxBytes]
		truncatedBySize = true
	}

	text, err := format(string(body))
	if err != nil {
		return "", fmt.Errorf("format content: %w", err)
	}

	if len(text) > maxChars {
		text = text[:maxChars]
		truncatedBySize = true
	}

	if truncatedBySize {
		text += "\n\n[truncated]"
	}

	return text, nil
}

// isTextContent checks whether the Content-Type header indicates text-based content.
func isTextContent(ct string) bool {
	if ct == "" {
		return true // assume text if not specified
	}
	ct = strings.ToLower(ct)
	for _, prefix := range []string{"text/", "application/json", "application/xml", "application/xhtml"} {
		if strings.Contains(ct, prefix) {
			return true
		}
	}
	return false
}

// isPrivateURL does a best-effort check for private/loopback addresses.
func isPrivateURL(rawURL string) bool {
	// Extract host from URL.
	host := rawURL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Not an IP literal — allow hostname-based URLs.
		return false
	}

	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

var reHTMLTag = regexp.MustCompile(`<[^>]*>`)
var reMultiWS = regexp.MustCompile(`\s{2,}`)

// defaultFormatter strips HTML tags and collapses whitespace.
func defaultFormatter(html string) (string, error) {
	html = reHTMLTag.ReplaceAllString(html, " ")
	html = reMultiWS.ReplaceAllString(html, " ")
	return strings.TrimSpace(html), nil
}
