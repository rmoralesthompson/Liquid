package straysubmit

// Straysubmit is a fixture component with a (submit) binding but no [hydroId]
// patch boundary.
type Straysubmit struct {
	CSRFToken string
}

// Selector returns the custom element tag for the component.
func (c *Straysubmit) Selector() string { return "app-straysubmit" }

// Save handles the form.
func (c *Straysubmit) Save() {}
