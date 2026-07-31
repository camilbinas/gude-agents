package ollama

// Models with reliable tool calling support.

func Qwen25(opts ...Option) (*OllamaProvider, error)     { return New("qwen2.5", opts...) }
func Qwen25_7B(opts ...Option) (*OllamaProvider, error)  { return New("qwen2.5:7b", opts...) }
func Qwen25_14B(opts ...Option) (*OllamaProvider, error) { return New("qwen2.5:14b", opts...) }
func Qwen25_32B(opts ...Option) (*OllamaProvider, error) { return New("qwen2.5:32b", opts...) }

func Llama32(opts ...Option) (*OllamaProvider, error)    { return New("llama3.2", opts...) }
func Llama32_1B(opts ...Option) (*OllamaProvider, error) { return New("llama3.2:1b", opts...) }
func Llama32_3B(opts ...Option) (*OllamaProvider, error) { return New("llama3.2:3b", opts...) }

func Mistral(opts ...Option) (*OllamaProvider, error)     { return New("mistral", opts...) }
func MistralNemo(opts ...Option) (*OllamaProvider, error) { return New("mistral-nemo", opts...) }

func Gemma3(opts ...Option) (*OllamaProvider, error)    { return New("gemma3", opts...) }
func Gemma3_4B(opts ...Option) (*OllamaProvider, error) { return New("gemma3:4b", opts...) }

func Phi4(opts ...Option) (*OllamaProvider, error) { return New("phi4", opts...) }

func Qwen36(opts ...Option) (*OllamaProvider, error)     { return New("qwen3.6", opts...) }
func Qwen35(opts ...Option) (*OllamaProvider, error)     { return New("qwen3.5", opts...) }
func Qwen3Coder(opts ...Option) (*OllamaProvider, error) { return New("qwen3-coder", opts...) }

// Tool calling is broken on some Ollama releases — the parser drops tool calls
// while streaming.
func Gemma4(opts ...Option) (*OllamaProvider, error) { return New("gemma4", opts...) }

func GPTOSS(opts ...Option) (*OllamaProvider, error)      { return New("gpt-oss", opts...) }
func GPTOSS_20B(opts ...Option) (*OllamaProvider, error)  { return New("gpt-oss:20b", opts...) }
func GPTOSS_120B(opts ...Option) (*OllamaProvider, error) { return New("gpt-oss:120b", opts...) }

// Tier aliases — map to Qwen 3.5/3.6 for consistent tool calling across tiers.
// Pull the tag first; Ollama has nothing cached for these on a fresh install.
func Cheapest(opts ...Option) (*OllamaProvider, error) { return New("qwen3.5:2b", opts...) }
func Standard(opts ...Option) (*OllamaProvider, error) { return New("qwen3.5:9b", opts...) }
func Smartest(opts ...Option) (*OllamaProvider, error) { return New("qwen3.6:27b", opts...) }
