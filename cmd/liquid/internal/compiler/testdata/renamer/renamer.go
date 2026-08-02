package renamer

import liquid "github.com/rmoralesthompson/liquid/core"

// Renamer renames a dashboard through a form.
type Renamer struct {
	HydroID   string
	CSRFToken string
	Title     string
}

// Selector returns the custom element tag for the component.
func (c *Renamer) Selector() string { return "app-renamer" }

// Rename handles the rename form.
func (c *Renamer) Rename(e liquid.Event) { c.Title = e.String("title") }
