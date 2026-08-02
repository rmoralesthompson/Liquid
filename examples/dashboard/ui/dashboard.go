// Package ui holds the dashboard's components — the directory liquid build
// compiles. Wiring that needs the generated Template methods lives in the
// parent main package, so a from-scratch build always type-checks.
package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Dashboard is the routed page: a static parent composing the interactive
// cards. It never re-renders after the initial response — each child with a
// HydroID patches itself — so child state survives for the life of the page
// (see the churn note on ticket #11).
type Dashboard struct {
	// Requests feeds the live metric; the dashboard reads it once at render
	// to seed the child's [reading] input.
	Requests *liquid.BehaviorSubject[int] `inject:""`
	// CurrentRate is the requests/sec value at render time.
	CurrentRate int
	// BoardName seeds the rename card's [name] input.
	BoardName string
	// StatLabel and StatValue feed the nested stat card's [input] bindings.
	StatLabel string
	StatValue string
}

// Selector returns the custom element tag for the component.
func (d *Dashboard) Selector() string { return "app-dashboard" }

// OnInit seeds the render from the metric subject's current value — a
// request-scoped read; the live subscription belongs to the metric child.
func (d *Dashboard) OnInit(liquid.Ctx) error {
	d.CurrentRate = d.Requests.Value()
	return nil
}

// Head sets the document title (D22).
func (d *Dashboard) Head() liquid.Head {
	return liquid.Head{Title: "Liquid — example dashboard"}
}
