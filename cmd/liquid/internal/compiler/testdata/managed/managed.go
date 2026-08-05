package managed

import liquid "github.com/rmoralesthompson/liquid/core"

// Managed follows a subject through the framework-owned Observe path (D25), so
// the subscription lifecycle is not the component's to manage — and the D29
// leak check must leave it alone.
type Managed struct {
	HydroID string
	Count   int
	orders  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag for the component.
func (c *Managed) Selector() string { return "app-managed" }

// Subscriptions declares the framework-owned bindings; the framework cancels
// each on session GC, so there is nothing for the component to unsubscribe.
func (c *Managed) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(c.orders, func(n int) { c.Count = n }),
	}
}
