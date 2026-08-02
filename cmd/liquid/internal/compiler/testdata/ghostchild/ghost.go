package ghostchild

// Ghost is a fixture parent whose template misspells its child's selector.
type Ghost struct {
	Owner string
}

// Selector returns the custom element tag for the component.
func (c *Ghost) Selector() string { return "app-ghost" }
