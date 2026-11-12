package webfetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func testConfig(blocked func(net.IP) bool) *config {
	return &config{
		timeout:      5 * time.Second,
		maxBytes:     defaultMaxBytes,
		maxRedirects: defaultMaxRedirects,
		maxChars:     defaultMaxChars,
		blocked:      blocked,
	}
}

func allowAll(net.IP) bool { return false }

// hostURL rewrites an httptest URL to use the "localhost" hostname so the
// request passes the IP-literal pre-check and exercises the dialer policy.
func hostURL(srv *httptest.Server) string {
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://localhost:" + port
}

func fetch(t *testing.T, cfg *config, rawURL string) (string, error) {
	t.Helper()
	return fetchPage(context.Background(), newGuardedClient(cfg), rawURL, cfg.maxBytes, cfg.maxChars, defaultFormatter)
}

func TestIsBlockedIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":        true,
		"10.1.2.3":         true,
		"172.16.0.1":       true,
		"192.168.1.1":      true,
		"169.254.169.254":  true, // cloud metadata
		"100.64.0.1":       true, // CGNAT
		"0.0.0.0":          true,
		"::1":              true,
		"::":               true,
		"fe80::1":          true,
		"fd00:ec2::254":    true, // AWS IPv6 metadata (ULA)
		"::ffff:127.0.0.1": true, // IPv4-mapped loopback
		"224.0.0.1":        true,
		"8.8.8.8":          false,
		"1.1.1.1":          false,
		"2606:4700::1111":  false,
	}
	for raw, want := range cases {
		if got := isBlockedIP(net.ParseIP(raw)); got != want {
			t.Errorf("isBlockedIP(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestIsPrivateURLHandlesIPv6AndPorts(t *testing.T) {
	cases := map[string]bool{
		"http://[::1]:8080/x":            true,
		"http://127.0.0.1:9000/":         true,
		"http://169.254.169.254/latest/": true,
		"https://example.com/":           false, // hostnames are checked at dial time
		"http://8.8.8.8/":                false,
	}
	for raw, want := range cases {
		if got := isPrivateURL(raw); got != want {
			t.Errorf("isPrivateURL(%s) = %v, want %v", raw, got, want)
		}
	}
}

// A hostname that resolves to loopback passes the literal pre-check and must be
// rejected by the dialer.
func TestDefaultPolicyBlocksHostnameResolvingToLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler must not be reached")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	_, err := fetch(t, testConfig(nil), "http://localhost:"+port+"/")
	if !errors.Is(err, errBlockedAddress) {
		t.Fatalf("err = %v, want errBlockedAddress", err)
	}
}

func TestRedirectToBlockedAddressIsRejected(t *testing.T) {
	ln6, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	internal := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("internal handler must not be reached")
	}))
	internal.Listener = ln6
	internal.Start()
	defer internal.Close()

	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/secret", http.StatusFound)
	}))
	defer public.Close()

	// Treat IPv4 loopback as "public" and IPv6 loopback as internal.
	policy := func(ip net.IP) bool { return ip.To4() == nil && ip.IsLoopback() }
	_, err = fetch(t, testConfig(policy), hostURL(public))
	if !errors.Is(err, errBlockedAddress) {
		t.Fatalf("err = %v, want errBlockedAddress", err)
	}
}

func TestAllowedFetchExtractsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>Hello</h1>  <p>world</p></body></html>"))
	}))
	defer srv.Close()

	got, err := fetch(t, testConfig(allowAll), hostURL(srv))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello world" {
		t.Fatalf("text = %q", got)
	}
}

func TestRedirectLimit(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, hostURL(srv)+r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()

	_, err := fetch(t, testConfig(allowAll), hostURL(srv)+"/")
	if err == nil || !strings.Contains(err.Error(), "stopped after 3 redirects") {
		t.Fatalf("err = %v, want redirect limit", err)
	}
}

func TestRejectsNonHTTPSchemeAndPrivateLiteral(t *testing.T) {
	cfg := testConfig(nil)
	if _, err := fetch(t, cfg, "file:///etc/passwd"); err == nil {
		t.Fatal("file scheme accepted")
	}
	if _, err := fetch(t, cfg, "http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("metadata literal accepted")
	}
}

func TestNonTextContentRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
	}))
	defer srv.Close()

	_, err := fetch(t, testConfig(allowAll), hostURL(srv))
	if err == nil || !strings.Contains(err.Error(), "non-text content type") {
		t.Fatalf("err = %v, want non-text rejection", err)
	}
}

// servePlain returns an httptest server that replies with the given body as
// text/plain, so the default formatter passes the bytes through unchanged.
func servePlain(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
}

func TestFetch_TruncateByCharsRuneSafe(t *testing.T) {
	// 100 three-byte runes. maxChars is a rune budget, so truncation must cut
	// on a rune boundary and keep valid UTF-8.
	body := strings.Repeat("語", 100)
	srv := servePlain(body)
	defer srv.Close()

	cfg := testConfig(allowAll)
	cfg.maxChars = 10
	out, err := fetchPage(context.Background(), newGuardedClient(cfg), hostURL(srv), cfg.maxBytes, cfg.maxChars, defaultFormatter)
	if err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8: %q", out)
	}
	if !strings.HasSuffix(out, "[truncated]") {
		t.Errorf("expected [truncated] marker, got %q", out)
	}
	text := strings.TrimSuffix(out, "\n\n[truncated]")
	if got := utf8.RuneCountInString(text); got != 10 {
		t.Errorf("want 10 runes kept, got %d (%q)", got, text)
	}
}

func TestFetch_TruncateByBytesRuneSafe(t *testing.T) {
	// The body exceeds maxBytes mid-rune. After the byte cut the formatter must
	// never see an invalid trailing rune, so the final output stays valid UTF-8.
	body := strings.Repeat("あ", 100) // 3 bytes each
	srv := servePlain(body)
	defer srv.Close()

	cfg := testConfig(allowAll)
	cfg.maxBytes = 10 // cuts inside the 4th rune (bytes 9-11)
	cfg.maxChars = defaultMaxChars
	out, err := fetchPage(context.Background(), newGuardedClient(cfg), hostURL(srv), cfg.maxBytes, cfg.maxChars, defaultFormatter)
	if err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8: %q", out)
	}
	if strings.ContainsRune(out, '\uFFFD') {
		t.Errorf("output contains the replacement rune: %q", out)
	}
}

func TestTrimPartialRune(t *testing.T) {
	full := []byte("あい") // 6 bytes, 2 runes
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"complete", full, "あい"},
		{"cut-after-first", full[:3], "あ"},
		{"cut-mid-second-1", full[:4], "あ"},
		{"cut-mid-second-2", full[:5], "あ"},
		{"ascii-untouched", []byte("hello"), "hello"},
		{"empty", []byte{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := trimPartialRune(c.in)
			if !utf8.Valid(got) {
				t.Fatalf("result not valid UTF-8: %q", got)
			}
			if string(got) != c.want {
				t.Errorf("trimPartialRune(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
