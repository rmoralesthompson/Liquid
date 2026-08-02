package inputmismatch

// Panel is a fixture parent binding an int field to a string child field.
type Panel struct {
	Count int
}

// Selector returns the custom element tag for the component.
func (c *Panel) Selector() string { return "app-panel" }
