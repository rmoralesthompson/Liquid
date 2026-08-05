package captured

import liquid "github.com/rmoralesthompson/liquid/core"

// Captured subscribes directly but keeps the cancel, so the framework cannot
// prove the subscription leaks — it is still outside the managed Subscriptions
// path, so the D29 check warns rather than errors.
type Captured struct {
	HydroID string
	Count   int
	cancels []func()
	orders  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag for the component.
func (c *Captured) Selector() string { return "app-captured" }

// OnInit captures the cancel, so the subscription is reachable — but nothing
// ties it to the session lifecycle.
func (c *Captured) OnInit() {
	c.cancels = append(c.cancels, c.orders.Subscribe(func(n int) { c.Count = n }))
}
