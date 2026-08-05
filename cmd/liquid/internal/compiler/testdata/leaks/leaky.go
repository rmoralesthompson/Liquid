package leaks

import liquid "github.com/rmoralesthompson/liquid/core"

// Leaky follows a subject by subscribing directly in OnInit, discarding the
// cancel — the D29 leak the vet check must flag.
type Leaky struct {
	HydroID string
	Count   int
	orders  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag for the component.
func (c *Leaky) Selector() string { return "app-leaky" }

// OnInit wires a bare subscription whose cancel is never captured, so nothing
// can release it when the session ends.
func (c *Leaky) OnInit() {
	c.orders.Subscribe(func(n int) { c.Count = n })
}
