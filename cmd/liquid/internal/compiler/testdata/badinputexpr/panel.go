package badinputexpr

// Panel is a fixture parent whose [input] wraps its expression in braces.
type Panel struct {
	Owner string
}

// Selector returns the custom element tag for the component.
func (c *Panel) Selector() string { return "app-panel" }
