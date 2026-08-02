package nocsrf

// Nocsrf is a fixture component with a form but no CSRFToken field for the
// framework to fill.
type Nocsrf struct {
	HydroID string
}

// Selector returns the custom element tag for the component.
func (c *Nocsrf) Selector() string { return "app-nocsrf" }

// Save handles the form.
func (c *Nocsrf) Save() {}
