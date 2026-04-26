package agentcore

// StoreMode selects how Remember stores facts in AgentCore.
type StoreMode int

const (
	// CreateEventMode sends facts as conversational events. AgentCore's
	// long-term memory strategies automatically extract and store insights.
	CreateEventMode StoreMode = iota

	// BatchCreateMode writes facts directly as memory records, bypassing
	// automatic extraction.
	BatchCreateMode
)

// Option configures a Store instance.
type Option func(*config)

type config struct {
	nsTmplStr   string        // namespace template string
	sessionIDFn func() string // session ID generator
	mode        StoreMode     // storage mode
}

// WithNamespaceTemplate sets a Go text/template string for generating the
// namespace from the actor ID. The template receives a struct with an ActorID
// field. Default: "/facts/{{.ActorID}}/".
func WithNamespaceTemplate(tmpl string) Option {
	return func(c *config) {
		c.nsTmplStr = tmpl
	}
}

// WithSessionIDFunc sets a function for generating session IDs used in
// CreateEvent mode. Default: uuid.NewString.
func WithSessionIDFunc(fn func() string) Option {
	return func(c *config) {
		c.sessionIDFn = fn
	}
}

// WithStoreMode selects between CreateEventMode and BatchCreateMode.
// Default: CreateEventMode.
func WithStoreMode(mode StoreMode) Option {
	return func(c *config) {
		c.mode = mode
	}
}
