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

// Tier aliases — map to Qwen 2.5 models for consistent tool calling across tiers.
func Cheapest(opts ...Option) (*OllamaProvider, error) { return New("qwen2.5:3b", opts...) }
func Standard(opts ...Option) (*OllamaProvider, error) { return New("qwen2.5:7b", opts...) }
func Smartest(opts ...Option) (*OllamaProvider, error) { return New("qwen2.5:32b", opts...) }
