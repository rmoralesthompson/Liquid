package hello

// Hello greets a named user.
type Hello struct {
	Name string
}

// Selector returns the custom element tag for the component.
func (c *Hello) Selector() string { return "app-hello" }
