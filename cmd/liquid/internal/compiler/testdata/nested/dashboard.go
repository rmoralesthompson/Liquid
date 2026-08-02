package nested

// Dashboard is a fixture parent component nesting a user card.
type Dashboard struct {
	Title string
	Owner string
}

// Selector returns the custom element tag for the component.
func (c *Dashboard) Selector() string { return "app-dashboard" }
