package deferexpr

// LateCard is a fixture deferred child.
type LateCard struct {
	HydroID string
}

// Selector returns the custom element tag for the component.
func (c *LateCard) Selector() string { return "app-late-card" }
