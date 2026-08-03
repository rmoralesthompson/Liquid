package app

// StatCard is the fixture child component.
type StatCard struct {
	Label string
}

// Selector returns the custom element tag for the component.
func (c *StatCard) Selector() string { return "app-stat-card" }
