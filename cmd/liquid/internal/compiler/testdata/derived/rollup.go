package derived

import liquid "github.com/rmoralesthompson/liquid/core"

// Rollup subscribes to a derived combinator (D25) directly and discards the
// cancel — the combinator arm of the D29 leak check, flagged exactly like a
// bare subject subscription.
type Rollup struct {
	HydroID string
	Total   int
	orders  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag for the component.
func (c *Rollup) Selector() string { return "app-rollup" }

// OnInit derives a total and subscribes to it directly, leaking the derived
// chain's framework-owned wiring along with the subscription.
func (c *Rollup) OnInit() {
	total := liquid.Map(c.orders, func(n int) int { return n * 2 })
	total.Subscribe(func(n int) { c.Total = n })
}
