package defernohydro

// PlainCard is a fixture child lacking the HydroID field a deferred swap
// needs.
type PlainCard struct {
	Note string
}

// Selector returns the custom element tag for the component.
func (c *PlainCard) Selector() string { return "app-plain-card" }
