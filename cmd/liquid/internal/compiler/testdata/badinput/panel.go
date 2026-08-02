package badinput

// Panel is a fixture parent whose [input] misspells the child's field.
type Panel struct {
	Owner string
}

// Selector returns the custom element tag for the component.
func (c *Panel) Selector() string { return "app-panel" }
