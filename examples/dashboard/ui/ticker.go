package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Quote is one row in the market ticker. Prices and changes are preformatted
// on the server (templates do no arithmetic), and Dir ("up"/"down") drives
// the row's colour class.
type Quote struct {
	Symbol string
	Price  string
	Change string
	Dir    string
}

// Ticker is the market-ticker card: a live rail of quotes pushed over SSE
// from the shared market subject. It carries no per-instance state — the
// template reads the current quotes straight off the injected subject, so the
// first (page-load) render and every pushed re-render draw the same source.
type Ticker struct {
	// HydroID marks the component interactive so pushes can target it.
	HydroID string
	// Market is the app-lifetime subject feeding the rail.
	Market *liquid.BehaviorSubject[[]Quote] `inject:""`
}

// MakeQuote formats a raw price and percentage change into a display Quote.
// The feed (in package main) computes the numbers; the ui package owns how
// they render.
func MakeQuote(symbol string, price, change float64) Quote {
	dir := "up"
	if change < 0 {
		dir = "down"
	}
	return Quote{Symbol: symbol, Price: usd(price), Change: pct(change), Dir: dir}
}

// Selector returns the custom element tag for the component.
func (t *Ticker) Selector() string { return "app-ticker" }

// Quotes is the template's *goFor source: the market's current snapshot.
func (t *Ticker) Quotes() []Quote { return t.Market.Value() }

// Subscriptions follows the market subject: each emission re-renders the rail
// (the render pulls the latest snapshot via Quotes), so apply is a no-op —
// the binding exists only to drive the push (D3/D20).
func (t *Ticker) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(t.Market, func([]Quote) {}),
	}
}
