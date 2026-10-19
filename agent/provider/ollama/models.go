package ollama

import "github.com/camilbinas/gude-agents/agent"

// modelProfile is what Gude knows about one model name, independent of how the
// caller constructs the provider.
type modelProfile struct {
	capabilities agent.ModelCapabilities
}

// catalog is the single source of truth for known Ollama model names. New
// consults it before applying caller options, so New("gpt-oss") and GPTOSS()
// are identical.
//
// Entries record only that Ollama's model library
// (https://ollama.com/library/<model>) lists the "tools" capability for the
// model. Context windows are deliberately left Unknown: the window a running
// server uses is its num_ctx setting, not the model's trained maximum, and
// Gude cannot see it. Models that are not listed (qwen2.5, mistral,
// mistral-nemo, gemma3, gemma4, phi4, qwen3.6, qwen3-coder and their tags) are
// intentionally Unknown for every field: their library pages do not state
// tool support in a form that was verified for this catalogue.
var catalog = map[string]modelProfile{
	"llama3.2":    {capabilities: toolUse},
	"llama3.2:1b": {capabilities: toolUse},
	"llama3.2:3b": {capabilities: toolUse},
	"qwen3.5":     {capabilities: toolUse},
	"gpt-oss":     {capabilities: toolUse},
	"gpt-oss:20b": {capabilities: toolUse},
	// gpt-oss:120b is the same family and carries the same library tags.
	"gpt-oss:120b": {capabilities: toolUse},
}

var toolUse = agent.ModelCapabilities{ToolUse: agent.Supported}

// Named Ollama models. Each delegates to New with its canonical model name; the
// catalog supplies the capability metadata. Pull a model before using it:
// Ollama has nothing cached for these on a fresh install.

// Models with reliable tool calling support.
func Qwen25(opts ...Option) (*Provider, error)     { return New("qwen2.5", opts...) }
func Qwen25_7B(opts ...Option) (*Provider, error)  { return New("qwen2.5:7b", opts...) }
func Qwen25_14B(opts ...Option) (*Provider, error) { return New("qwen2.5:14b", opts...) }
func Qwen25_32B(opts ...Option) (*Provider, error) { return New("qwen2.5:32b", opts...) }
func Llama32(opts ...Option) (*Provider, error)    { return New("llama3.2", opts...) }
func Llama32_1B(opts ...Option) (*Provider, error) { return New("llama3.2:1b", opts...) }
func Llama32_3B(opts ...Option) (*Provider, error) { return New("llama3.2:3b", opts...) }

func Mistral(opts ...Option) (*Provider, error)     { return New("mistral", opts...) }
func MistralNemo(opts ...Option) (*Provider, error) { return New("mistral-nemo", opts...) }

func Gemma3(opts ...Option) (*Provider, error)    { return New("gemma3", opts...) }
func Gemma3_4B(opts ...Option) (*Provider, error) { return New("gemma3:4b", opts...) }

func Phi4(opts ...Option) (*Provider, error) { return New("phi4", opts...) }

func Qwen36(opts ...Option) (*Provider, error)     { return New("qwen3.6", opts...) }
func Qwen35(opts ...Option) (*Provider, error)     { return New("qwen3.5", opts...) }
func Qwen3Coder(opts ...Option) (*Provider, error) { return New("qwen3-coder", opts...) }

// Tool calling is broken on some Ollama releases — the parser drops tool calls
// while streaming.
func Gemma4(opts ...Option) (*Provider, error) { return New("gemma4", opts...) }

func GPTOSS(opts ...Option) (*Provider, error)      { return New("gpt-oss", opts...) }
func GPTOSS_20B(opts ...Option) (*Provider, error)  { return New("gpt-oss:20b", opts...) }
func GPTOSS_120B(opts ...Option) (*Provider, error) { return New("gpt-oss:120b", opts...) }
