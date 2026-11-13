package agent

// Capability describes whether a configured provider supports a feature.
//
// Unknown means Gude has no reliable metadata and should normally attempt the
// operation. Unsupported is an explicit incompatibility that framework
// features may reject before making a provider request.
type Capability uint8

const (
	// Unknown means the configured provider/model capability is not known.
	Unknown Capability = iota
	// Supported means the configured provider/model supports the capability.
	Supported
	// Unsupported means the configured provider/model is known not to support
	// the capability through Gude's provider adapter.
	Unsupported
)

// ToolChoiceCapabilities describes the tool-choice modes supported by a
// configured provider/model combination.
type ToolChoiceCapabilities struct {
	// Auto lets the model decide whether to call a supplied tool.
	Auto Capability
	// Required requires the model to call one of the supplied tools.
	Required Capability
	// Specific requires the model to call one specifically named supplied tool.
	Specific Capability
}

// ModelCapabilities describes the effective capabilities of a configured
// provider/model combination: the selected model, its provider API, and
// Gude's adapter implementation.
//
// Numeric limits are advisory metadata. They are intentionally not hard
// engine enforcement boundaries because provider overhead, reasoning behavior,
// token estimation, and provider limits can change independently.
type ModelCapabilities struct {
	// ContextWindowTokens is the total context capacity where known.
	ContextWindowTokens int
	// MaxOutputTokens is the independently documented output-token ceiling
	// where known.
	MaxOutputTokens int

	ToolUse    Capability
	ToolChoice ToolChoiceCapabilities

	// NativeStructuredOutput reports whether this configured provider/model
	// supports a native schema/JSON response mechanism implemented by Gude,
	// rather than emulating structured output through tool calling.
	NativeStructuredOutput Capability
}

// CapabilityProvider is an optional Provider extension that reports effective
// model capabilities. Providers that do not implement it remain fully
// compatible and are treated as reporting unknown capabilities.
type CapabilityProvider interface {
	Capabilities() ModelCapabilities
}

// CapabilitiesOf returns a provider's effective capabilities, or the zero
// value (all fields Unknown/zero) when the provider is nil or does not expose
// capability metadata.
func CapabilitiesOf(p Provider) ModelCapabilities {
	if p == nil {
		return ModelCapabilities{}
	}
	if cp, ok := p.(CapabilityProvider); ok {
		return cp.Capabilities()
	}
	return ModelCapabilities{}
}

// MergeModelCapabilities applies a partial capability override to base.
// Non-zero numeric values and non-Unknown capability values replace their
// counterparts in base. It deliberately cannot reset a field to Unknown;
// provider packages expose fine-grained options for explicit resets.
func MergeModelCapabilities(base, override ModelCapabilities) ModelCapabilities {
	if override.ContextWindowTokens != 0 {
		base.ContextWindowTokens = override.ContextWindowTokens
	}
	if override.MaxOutputTokens != 0 {
		base.MaxOutputTokens = override.MaxOutputTokens
	}
	if override.ToolUse != Unknown {
		base.ToolUse = override.ToolUse
	}
	if override.ToolChoice.Auto != Unknown {
		base.ToolChoice.Auto = override.ToolChoice.Auto
	}
	if override.ToolChoice.Required != Unknown {
		base.ToolChoice.Required = override.ToolChoice.Required
	}
	if override.ToolChoice.Specific != Unknown {
		base.ToolChoice.Specific = override.ToolChoice.Specific
	}
	if override.NativeStructuredOutput != Unknown {
		base.NativeStructuredOutput = override.NativeStructuredOutput
	}
	return base
}
