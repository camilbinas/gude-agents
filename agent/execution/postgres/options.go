package postgres

const defaultTable = "executions"

// Option configures a Store.
type Option func(*config)

type config struct {
	table string
}

// WithTableName sets the table that holds executions. An empty name keeps the
// default, "executions".
func WithTableName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.table = name
		}
	}
}
