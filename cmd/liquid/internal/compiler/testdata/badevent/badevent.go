package badevent

import liquid "github.com/rmoralesthompson/liquid/core"

// Badevent is a fixture component whose (submit) handler returns an error,
// which is not a dispatchable shape.
type Badevent struct {
	HydroID   string
	CSRFToken string
}

// Selector returns the custom element tag for the component.
func (c *Badevent) Selector() string { return "app-badevent" }

// Rename has the wrong shape for a handler: handlers return nothing.
func (c *Badevent) Rename(e liquid.Event) error { return nil }
