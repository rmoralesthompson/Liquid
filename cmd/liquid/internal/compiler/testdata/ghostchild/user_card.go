package ghostchild

// UserCard is the child component the template meant to nest.
type UserCard struct {
	Name string
}

// Selector returns the custom element tag for the component.
func (c *UserCard) Selector() string { return "app-user-card" }
