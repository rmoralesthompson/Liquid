package ui

// Counter is the (click) card: one button, one server round-trip per click.
type Counter struct {
	// HydroID marks the component interactive; the runtime patches the
	// element carrying it.
	HydroID string
	// Count is the number of clicks so far.
	Count int
}

// Selector returns the custom element tag for the component.
func (c *Counter) Selector() string { return "app-counter" }

// Increment handles the +1 button.
func (c *Counter) Increment() { c.Count++ }
