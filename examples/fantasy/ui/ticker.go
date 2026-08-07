package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Ticker is the live news-and-stats rail pinned to the bottom of the page, fed
// a rolling window of fictional gameday items over SSE. New items are pushed as
// they "happen"; the newest is highlighted as it arrives.
type Ticker struct {
	// HydroID marks the rail interactive so pushes can target it.
	HydroID string
	// Feed is the app-lifetime subject carrying the latest ticker items.
	Feed *liquid.BehaviorSubject[[]TickerItem] `inject:""`
}

func (t *Ticker) Selector() string { return "app-ticker" }

// Items is the template's *goFor source: the current rolling window.
func (t *Ticker) Items() []TickerItem { return t.Feed.Value() }

// Subscriptions drives the live push; the render pulls the latest window via
// Items, so apply is a no-op.
func (t *Ticker) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(t.Feed, func([]TickerItem) {})}
}
