package mcp

import (
	"context"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBuildEnv_AllowlistUnix(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "LANG=C", "TMPDIR=/t", "AWS_SECRET_ACCESS_KEY=x", "OPENAI_API_KEY=y", "USER=u", "SHELL=/bin/sh", "BROKEN"}
	got := buildEnv(parent, "linux", false, []string{"API_KEY=explicit"})
	joined := strings.Join(got, "\n")
	for _, bad := range []string{"AWS_SECRET", "OPENAI_API_KEY", "BROKEN"} {
		if strings.Contains(joined, bad) {
			t.Errorf("%s leaked into env: %v", bad, got)
		}
	}
	for _, want := range []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "LANG=C", "TMPDIR=/t", "USER=u", "SHELL=/bin/sh", "API_KEY=explicit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
}

func TestBuildEnv_Windows(t *testing.T) {
	got := buildEnv([]string{"SystemRoot=C:\\Windows", "Path=C:\\x", "TEMP=C:\\t", "SECRET=1"}, "windows", false, nil)
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestBuildEnv_Inherit(t *testing.T) {
	got := buildEnv([]string{"SECRET=1", "PATH=/bin"}, "linux", true, []string{"X=1"})
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestSubprocessEnv_NoLeak(t *testing.T) {
	t.Setenv("GUDE_TEST_SECRET", "s3cret")
	for _, kv := range subprocessEnv(false, nil) {
		if strings.HasPrefix(kv, "GUDE_TEST_SECRET=") {
			t.Fatal("secret leaked")
		}
	}
	found := false
	for _, kv := range subprocessEnv(true, nil) {
		if strings.HasPrefix(kv, "GUDE_TEST_SECRET=") {
			found = true
		}
	}
	if !found {
		t.Fatal("WithInheritEnv semantics broken")
	}
}

func TestWithInheritEnvOption(t *testing.T) {
	cfg := newClientConfig([]Option{WithInheritEnv(), WithMaxOutputBytes(10), WithImageLimits(1, 2)})
	if !cfg.inheritEnv || cfg.limits.maxBytes != 10 || cfg.limits.maxImages != 1 || cfg.limits.maxImageBytes != 2 {
		t.Fatalf("%+v", cfg)
	}
	if newClientConfig([]Option{WithMaxOutputBytes(0)}).limits.maxBytes != DefaultMaxOutputBytes {
		t.Fatal("clamp to default")
	}
}

func TestToOutputLimited_TruncatesText(t *testing.T) {
	big := strings.Repeat("é", 100)
	out := toOutputLimited([]sdkmcp.Content{&sdkmcp.TextContent{Text: big}}, outputLimits{maxBytes: 51, maxImages: 1, maxImageBytes: 10})
	if !strings.Contains(out.Text, "[truncated:") || strings.Index(out.Text, "\n") > 51 {
		t.Fatalf("not truncated: %q", out.Text)
	}
	if !strings.HasPrefix(out.Text, strings.Repeat("é", 25)) {
		t.Fatal("rune boundary broken")
	}
}

func TestToOutputLimited_ImageCaps(t *testing.T) {
	imgs := []sdkmcp.Content{
		&sdkmcp.ImageContent{Data: []byte("ok"), MIMEType: "image/png"},
		&sdkmcp.ImageContent{Data: []byte("toolargeimage"), MIMEType: "image/png"},
		&sdkmcp.ImageContent{Data: []byte("a"), MIMEType: "image/png"},
		&sdkmcp.ImageContent{Data: []byte("b"), MIMEType: "image/png"},
	}
	out := toOutputLimited(imgs, outputLimits{maxBytes: 1000, maxImages: 2, maxImageBytes: 5})
	if len(out.Images) != 2 {
		t.Fatalf("images=%d", len(out.Images))
	}
	if !strings.Contains(out.Text, "exceeds limit") || !strings.Contains(out.Text, "limit of 2 images") {
		t.Fatalf("text=%q", out.Text)
	}
}

func TestPoolIsErrorTruncated(t *testing.T) {
	srv := &fakeServer{}
	p := newFakePool(1, srv)
	p.connect = func(context.Context) (poolSession, error) {
		return errSession{strings.Repeat("x", 100000)}, nil
	}
	tl := workTool(t, p)
	_, err := tl.Handler(context.Background(), []byte(`{}`))
	if err == nil || len(err.Error()) > 3*1024 {
		t.Fatalf("err len=%d", len(err.Error()))
	}
}

type errSession struct{ text string }

func (e errSession) CallTool(context.Context, *sdkmcp.CallToolParams) (*sdkmcp.CallToolResult, error) {
	return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: e.text}}}, nil
}
func (errSession) Close() error { return nil }

func TestToolsConfig_UnknownNames(t *testing.T) {
	cfg, err := newToolsConfig([]ToolsOption{IncludeTools("a", "typo")})
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.checkKnown(map[string]struct{}{"a": {}, "b": {}})
	if err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("err=%v", err)
	}
	cfg, _ = newToolsConfig([]ToolsOption{ExcludeTools("a")})
	if err := cfg.checkKnown(map[string]struct{}{"a": {}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newToolsConfig([]ToolsOption{IncludeTools(), ExcludeTools()}); err == nil {
		t.Fatal("expected mutual exclusion error")
	}
}

func TestPoolTools_Filtering(t *testing.T) {
	p := newFakePool(1, &fakeServer{})
	p.toolDefs = []*sdkmcp.Tool{{Name: "a"}, {Name: "b"}}
	got, err := p.Tools(IncludeTools())
	if err != nil || len(got) != 0 {
		t.Fatalf("empty include: %v %v", got, err)
	}
	got, err = p.Tools(IncludeTools("a"), WithToolPrefix("p_"))
	if err != nil || len(got) != 1 || got[0].Spec.Name != "p_a" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := p.Tools(IncludeTools("zzz")); err == nil {
		t.Fatal("expected unknown error")
	}
	got, err = p.Tools(ExcludeTools("a"))
	if err != nil || len(got) != 1 || got[0].Spec.Name != "b" {
		t.Fatalf("%v %v", got, err)
	}
}
