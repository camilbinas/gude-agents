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
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// defaults.
const (
	defaultTimeout      = 15 * time.Second
	defaultMaxBytes     = 32 * 1024 // 32 KB
	defaultMaxRedirects = 3
	defaultMaxChars     = 4000
	defaultDialTimeout  = 5 * time.Second
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

	// blocked reports whether a resolved address must not be dialed. Nil means
	// isBlockedIP. Tests substitute it to reach httptest servers on loopback.
	blocked func(net.IP) bool
}

// WithTimeout sets the HTTP request timeout. Default: 15s. A value <= 0 keeps
// the default so a request can never run without a deadline.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithMaxBytes sets the maximum response body size in bytes. Default: 32KB.
// A value <= 0 keeps the default.
func WithMaxBytes(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxBytes = n
		}
	}
}

// WithMaxRedirects sets the maximum number of redirects to follow. Default: 3.
// A negative value keeps the default; 0 disables redirects.
func WithMaxRedirects(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.maxRedirects = n
		}
	}
}

// WithMaxChars sets the maximum number of characters in the returned text.
// Content beyond this limit is truncated with a "[truncated]" marker.
// Default: 4000. A value <= 0 keeps the default.
func WithMaxChars(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxChars = n
		}
	}
}

// WithClient sets a custom HTTP client. When set, timeout and redirect
// options are ignored — the caller is responsible for configuring them
// on the provided client.
//
// A custom client also replaces the built-in SSRF protection, which lives in
// the default client's dialer: the tool still rejects private and loopback IP
// literals in the requested URL, but hostnames, DNS answers and redirects are
// no longer checked. Use a custom client only when its transport enforces an
// equivalent egress policy.
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
		client = newGuardedClient(cfg)
	}

	maxBytes := cfg.maxBytes
	maxChars := cfg.maxChars
	format := cfg.formatter

	if format == nil {
		format = defaultFormatter
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch",
			},
		},
		"required": []any{"url"},
	}
	return tool.NewRaw(
		"web_fetch",
		"Fetch a web page and return its text content. "+
			"Use after a web search to read a specific result in detail.",
		schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var req struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return "", err
			}
			log := tool.LoggerFrom(ctx)
			log.Logf("fetching %s", redactURL(req.URL))
			result, err := fetchPage(ctx, client, req.URL, maxBytes, maxChars, format)
			if err != nil {
				return "", err
			}
			log.Logf("fetched %d chars", len(result))
			return result, nil
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
		// Drop any trailing partial UTF-8 rune left by the byte cut.
		body = trimPartialRune(body[:maxBytes])
		truncatedBySize = true
	}

	text, err := format(string(body))
	if err != nil {
		return "", fmt.Errorf("format content: %w", err)
	}

	// Truncate by rune count so a multibyte character is not split.
	if runes := []rune(text); len(runes) > maxChars {
		text = string(runes[:maxChars])
		truncatedBySize = true
	}

	if truncatedBySize {
		text += "\n\n[truncated]"
	}

	return text, nil
}

// trimPartialRune drops a trailing incomplete UTF-8 rune left by a byte-length
// cut, leaving valid UTF-8 untouched.
func trimPartialRune(b []byte) []byte {
	for len(b) > 0 && !utf8.Valid(b) {
		// Walk back over continuation bytes (0b10xxxxxx) and the lead byte.
		i := len(b) - 1
		for i > 0 && b[i]&0xC0 == 0x80 {
			i--
		}
		if utf8.Valid(b[:i]) {
			return b[:i]
		}
		b = b[:i]
	}
	return b
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

// errBlockedAddress is returned when a connection would reach a non-public
// address.
var errBlockedAddress = errors.New("destination resolves to a private, loopback or otherwise non-public address")

// newGuardedClient builds the default HTTP client. SSRF protection is enforced
// in the dialer's Control hook, which runs after DNS resolution for every
// connection attempt, including each redirect hop, so neither a hostname that
// resolves to an internal address nor a redirect to one can bypass it. The
// proxy is disabled because a proxied request would dial the proxy instead of
// the destination and skip the check.
func newGuardedClient(cfg *config) *http.Client {
	blocked := cfg.blocked
	if blocked == nil {
		blocked = isBlockedIP
	}
	dialTimeout := defaultDialTimeout
	if cfg.timeout > 0 && cfg.timeout < dialTimeout {
		dialTimeout = cfg.timeout
	}
	dialer := &net.Dialer{
		Timeout: dialTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("web_fetch: dial %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil || blocked(ip) {
				return fmt.Errorf("web_fetch: dial %s: %w", host, errBlockedAddress)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: cfg.timeout,
		ExpectContinueTimeout: time.Second,
	}
	maxRedirects := cfg.maxRedirects
	return &http.Client{
		Timeout:   cfg.timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
				return errors.New("redirect from https to http refused")
			}
			return nil
		},
	}
}

// blockedNets lists non-public ranges that net.IP helpers do not cover.
var blockedNets = mustCIDRs(
	"0.0.0.0/8",       // "this" network
	"100.64.0.0/10",   // RFC 6598 CGNAT / shared address space
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"192.88.99.0/24",  // deprecated 6to4 relay anycast
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"240.0.0.0/4",     // reserved + broadcast
	"::/96",           // deprecated IPv4-compatible
	"64:ff9b:1::/48",  // local-use NAT64
	"100::/64",        // discard prefix
	"2001::/32",       // Teredo
	"2001:db8::/32",   // documentation
	"fec0::/10",       // deprecated site-local
)

var (
	nat64Net  = mustCIDRs("64:ff9b::/96")[0]
	sixToFour = mustCIDRs("2002::/16")[0]
)

func mustCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, len(cidrs))
	for i, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out[i] = n
	}
	return out
}

// isBlockedIP reports whether ip is not a public unicast address. IPv4-mapped
// IPv6 addresses are unmapped first, and addresses that embed an IPv4 address
// (NAT64 64:ff9b::/96, 6to4 2002::/16) are blocked when the embedded address
// is blocked.
func isBlockedIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	} else if len(ip) != net.IPv6len {
		return true // malformed
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	if len(ip) == net.IPv6len {
		switch {
		case nat64Net.Contains(ip):
			return isBlockedIP(net.IP(ip[12:16]))
		case sixToFour.Contains(ip):
			return isBlockedIP(net.IP(ip[2:6]))
		}
	}
	return false
}

// redactURL returns scheme://host only, dropping path, query and userinfo,
// which may carry tokens.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<invalid url>"
	}
	return u.Scheme + "://" + u.Host
}

// isPrivateURL is a fast pre-flight rejection of private IP literals. It is
// not the security boundary: the guarded client's dialer checks every
// resolved address.
func isPrivateURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false // http.NewRequest reports the parse error
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return false // hostname: checked by the dialer after resolution
	}
	return isBlockedIP(ip)
}

var reHTMLTag = regexp.MustCompile(`<[^>]*>`)
var reMultiWS = regexp.MustCompile(`\s{2,}`)

// defaultFormatter strips HTML tags and collapses whitespace.
func defaultFormatter(html string) (string, error) {
	html = reHTMLTag.ReplaceAllString(html, " ")
	html = reMultiWS.ReplaceAllString(html, " ")
	return strings.TrimSpace(html), nil
}
