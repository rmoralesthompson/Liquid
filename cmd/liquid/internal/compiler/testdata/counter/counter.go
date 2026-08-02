package counter

// Counter counts button clicks.
type Counter struct {
	HydroID string
	Count   int
}

// Selector returns the custom element tag for the component.
func (c *Counter) Selector() string { return "app-counter" }

// Increment handles the +1 button.
func (c *Counter) Increment() { c.Count++ }
