package app

// Dashboard is the fixture parent component.
type Dashboard struct {
	HydroID string
	// Title is the dashboard heading.
	Title   string
	Logs    []string
	IsAdmin bool
}

// Selector returns the custom element tag for the component.
func (c *Dashboard) Selector() string { return "app-dashboard" }

// Refresh reloads the dashboard state.
func (c *Dashboard) Refresh() { c.Logs = nil }
