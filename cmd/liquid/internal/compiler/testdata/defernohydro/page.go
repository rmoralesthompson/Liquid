package defernohydro

// Page is a fixture parent deferring a child with no patch boundary.
type Page struct{}

// Selector returns the custom element tag for the component.
func (c *Page) Selector() string { return "app-page" }
