package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Admin is the guarded page (D4/D19): the route guard runs before this
// component is ever instantiated.
type Admin struct{}

// Selector returns the custom element tag for the component.
func (a *Admin) Selector() string { return "app-admin" }

// Head sets the document title (D22).
func (a *Admin) Head() liquid.Head {
	return liquid.Head{Title: "Liquid — admin"}
}
