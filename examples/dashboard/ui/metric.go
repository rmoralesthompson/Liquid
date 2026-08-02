package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Metric is the SSE card: it follows the shared requests/sec subject and is
// re-rendered over the session's SSE stream on every emission (D3). Children
// skip OnInit, so the initial reading arrives as an [input] from the parent.
type Metric struct {
	// HydroID marks the component interactive so pushes can target it.
	HydroID string
	// Requests is the app-lifetime subject driving the card.
	Requests *liquid.BehaviorSubject[int] `inject:""`
	// Reading is the displayed requests/sec value.
	Reading int
}

// Selector returns the custom element tag for the component.
func (m *Metric) Selector() string { return "app-metric" }

// Subscriptions declares the live binding: every emission updates Reading
// and pushes the re-render (D20 — latest state, not history).
func (m *Metric) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(m.Requests, func(v int) { m.Reading = v }),
	}
}
