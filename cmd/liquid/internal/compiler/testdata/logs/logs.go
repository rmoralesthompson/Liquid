package logs

// Logs lists recent log entries.
type Logs struct {
	Entries []string
}

// Selector returns the custom element tag for the component.
func (c *Logs) Selector() string { return "app-logs" }
