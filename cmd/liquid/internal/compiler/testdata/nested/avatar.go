package nested

// Avatar is a fixture grandchild, nested by UserCard.
type Avatar struct {
	Initials string
}

// Selector returns the custom element tag for the component.
func (c *Avatar) Selector() string { return "app-avatar" }
