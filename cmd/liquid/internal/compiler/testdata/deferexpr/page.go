package deferexpr

// Page is a fixture parent giving *liquidDefer a value it does not take.
type Page struct{}

// Selector returns the custom element tag for the component.
func (c *Page) Selector() string { return "app-page" }
