package childclick

// Panel is a fixture parent that wrongly binds (click) on a child selector.
type Panel struct {
	HydroID string
	Owner   string
}

// Selector returns the custom element tag for the component.
func (c *Panel) Selector() string { return "app-panel" }

// Refresh handles the misplaced click binding.
func (c *Panel) Refresh() {}
