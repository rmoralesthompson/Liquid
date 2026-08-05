package liquidtest_test

import (
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// dashboardTile is the D25 shape: a tile whose value is *derived* from two
// app-lifetime sources via CombineLatest — an order count and a rate control
// (a filter). The tile never touches the derived value's subscription
// lifecycle; the framework owns it. This is the runtime seam (Stream()) that
// proves a combinator composes with server push end to end, with no
// wire-format or transport change.
type dashboardTile struct {
	HydroID string
	Orders  *liquid.BehaviorSubject[int]     `inject:""`
	Rate    *liquid.BehaviorSubject[float64] `inject:""`
	total   *liquid.Derived[int]
	Total   int
}

func (d *dashboardTile) Selector() string { return "app-dashboard-tile" }

func (d *dashboardTile) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="total">{{ .Total }}</span></div>`
}

// OnInit derives the tile's value; it runs before Subscriptions() on the same
// instance, so the binding observes the derived value.
func (d *dashboardTile) OnInit(liquid.Ctx) error {
	d.total = liquid.CombineLatest(d.Orders, d.Rate, func(o int, r float64) int {
		return int(float64(o) * r)
	})
	d.Total = d.total.Value()
	return nil
}

func (d *dashboardTile) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(d.total, func(v int) { d.Total = v })}
}

func newDashboardHarness(t *testing.T, orders *liquid.BehaviorSubject[int], rate *liquid.BehaviorSubject[float64]) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Provide(orders); err != nil {
		t.Fatalf("Provide orders: %v", err)
	}
	if err := app.Provide(rate); err != nil {
		t.Fatalf("Provide rate: %v", err)
	}
	if err := app.Route("/", &dashboardTile{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestDerivedTileRendersItsCombinedValue(t *testing.T) {
	orders := liquid.NewBehaviorSubject(10)
	rate := liquid.NewBehaviorSubject(2.0)
	h := newDashboardHarness(t, orders, rate)

	page := h.Get("/")
	if got := page.Text("#total"); got != "20" {
		t.Fatalf("initial render #total = %q, want 20 (10 orders * rate 2.0)", got)
	}
}

func TestDerivedTilePushesWhenTheFilterChanges(t *testing.T) {
	orders := liquid.NewBehaviorSubject(10)
	rate := liquid.NewBehaviorSubject(2.0)
	h := newDashboardHarness(t, orders, rate)

	page := h.Get("/")
	stream := h.Stream()
	defer stream.Close()

	// Changing ONE input — the rate control — must recompute the derived tile
	// and push the re-render over SSE. This is CombineLatest fanning a single
	// filter control out to a dependent tile with no hand-wired subscription
	// (D25), carried on the unchanged D3 push transport.
	rate.Next(3.0)

	awaitPush(t, stream, page.HydroID(), "#total", "30") // 10 * 3.0
}

func TestDerivedTilePushesWhenTheUpstreamSourceChanges(t *testing.T) {
	orders := liquid.NewBehaviorSubject(10)
	rate := liquid.NewBehaviorSubject(2.0)
	h := newDashboardHarness(t, orders, rate)

	page := h.Get("/")
	stream := h.Stream()
	defer stream.Close()

	// The other input changing pushes too: CombineLatest recomputes on either.
	orders.Next(25)

	awaitPush(t, stream, page.HydroID(), "#total", "50") // 25 * 2.0
}
