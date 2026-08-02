package badge

// Badge shows an administrator badge for privileged users.
type Badge struct {
	IsAdmin bool
}

// Selector returns the custom element tag for the component.
func (c *Badge) Selector() string { return "app-badge" }
