package ui

// StatCard is the nested-card demo: an interactive child fed by [input]
// bindings from the parent, with its own (click) state. Pinning survives
// because the parent is static and never re-renders over it.
type StatCard struct {
	// HydroID marks the card interactive.
	HydroID string
	// Label and Value arrive from the parent's [label]/[value] inputs.
	Label string
	Value string
	// Pinned is the card's own toggled state.
	Pinned bool
}

// Selector returns the custom element tag for the component.
func (s *StatCard) Selector() string { return "app-stat-card" }

// TogglePin handles the pin button.
func (s *StatCard) TogglePin() { s.Pinned = !s.Pinned }
