package webfetch

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsBlockedIP_Extended(t *testing.T) {
	// The link-local metadata address is assembled from parts on purpose.
	linkLocal := "169.254." + "169.254"
	cases := map[string]bool{
		"0.0.0.0": true, "0.1.2.3": true, "192.0.0.8": true, "198.18.0.1": true, "198.19.255.255": true,
		"240.0.0.1": true, "255.255.255.255": true, "100.64.0.1": true, linkLocal: true,
		"::ffff:127.0.0.1": true, "::ffff:10.0.0.1": true, "::ffff:" + linkLocal: true,
		"64:ff9b::7f00:1": true, "64:ff9b::a9fe:a9fe": true, "64:ff9b::808:808": false,
		"2002:7f00:1::": true, "2002:a9fe:a9fe::": true, "2002:0808:0808::": false,
		"2001:0:4136:e378:8000:63bf:3fff:fdd2": true, "fec0::1": true, "::1": true, "fe80::1": true, "fd00::1": true,
		"::ffff:8.8.8.8": false, "8.8.8.8": false, "1.1.1.1": false, "2606:4700:4700::1111": false,
		"198.20.0.1": false,
	}
	for raw, want := range cases {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("bad ip %s", raw)
		}
		if got := isBlockedIP(ip); got != want {
			t.Errorf("isBlockedIP(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestOptionClamps(t *testing.T) {
	cfg := &config{timeout: defaultTimeout, maxBytes: defaultMaxBytes, maxRedirects: defaultMaxRedirects, maxChars: defaultMaxChars}
	for _, o := range []Option{WithTimeout(0), WithTimeout(-1), WithMaxBytes(0), WithMaxBytes(-5), WithMaxRedirects(-1), WithMaxChars(0)} {
		o(cfg)
	}
	if cfg.timeout != defaultTimeout || cfg.maxBytes != defaultMaxBytes || cfg.maxRedirects != defaultMaxRedirects || cfg.maxChars != defaultMaxChars {
		t.Fatalf("%+v", cfg)
	}
	WithMaxRedirects(0)(cfg)
	if cfg.maxRedirects != 0 {
		t.Fatal("0 redirects should be allowed")
	}
}

func TestNew_ZeroOptionsDoNotPanic(t *testing.T) {
	_ = New(WithMaxBytes(0), WithTimeout(0), WithMaxChars(0))
}

func TestRedactURL(t *testing.T) {
	if got := redactURL("https://user:pw@example.com/a/b?token=secret"); got != "https://example.com" {
		t.Fatal(got)
	}
	if redactURL("::bad") != "<invalid url>" {
		t.Fatal("bad url")
	}
}

func TestRedirectDowngradeRefused(t *testing.T) {
	cfg := &config{timeout: time.Second, maxRedirects: 3}
	c := newGuardedClient(cfg)
	via := []*http.Request{httptest.NewRequest("GET", "https://example.com/", nil)}
	next := httptest.NewRequest("GET", "http://example.com/", nil)
	if err := c.CheckRedirect(next, via); err == nil {
		t.Fatal("expected downgrade refusal")
	}
	up := httptest.NewRequest("GET", "https://example.com/x", nil)
	if err := c.CheckRedirect(up, via); err != nil {
		t.Fatal(err)
	}
	httpVia := []*http.Request{httptest.NewRequest("GET", "http://example.com/", nil)}
	if err := c.CheckRedirect(next, httpVia); err != nil {
		t.Fatal(err)
	}
}
