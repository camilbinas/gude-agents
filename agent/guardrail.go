package agent

// InputGuardrail validates/transforms the user message before it reaches the Provider.
type InputGuardrail func(c *Context, message string) (string, error)

// OutputGuardrail validates/transforms the final response before returning to the caller.
type OutputGuardrail func(c *Context, response string) (string, error)
