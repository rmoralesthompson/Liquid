package straydefer

// Banner is a fixture misusing *liquidDefer on a plain element.
type Banner struct {
	Label string
}

// Selector returns the custom element tag for the component.
func (c *Banner) Selector() string { return "app-banner" }
