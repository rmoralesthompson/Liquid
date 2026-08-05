package allowsub

import liquid "github.com/rmoralesthompson/liquid/core"

// Allowsub deliberately subscribes directly and suppresses the D29 check,
// asserting it owns the teardown by hand — the rare case the ticket keeps
// suppressible. Both a trailing and a preceding directive comment silence it.
type Allowsub struct {
	HydroID string
	Count   int
	orders  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag for the component.
func (c *Allowsub) Selector() string { return "app-allowsub" }

// OnInit wires two bare subscriptions that would each be a provable leak, each
// silenced by an allow-subscribe directive.
func (c *Allowsub) OnInit() {
	c.orders.Subscribe(func(n int) { c.Count = n }) //liquid:allow-subscribe
	//liquid:allow-subscribe
	c.orders.Subscribe(func(n int) { c.Count = n })
}
