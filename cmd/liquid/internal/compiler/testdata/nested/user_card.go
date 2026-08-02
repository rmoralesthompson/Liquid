package nested

// UserCard is a fixture child component receiving its name by [input].
type UserCard struct {
	Name string
}

// Selector returns the custom element tag for the component.
func (c *UserCard) Selector() string { return "app-user-card" }
