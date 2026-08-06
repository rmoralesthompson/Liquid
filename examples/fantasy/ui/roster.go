package ui

import (
	"fmt"
	"strconv"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// Roster is the live lineup card: a rail of player rows pushed over SSE from
// the shared board subject. Like the dashboard's ticker it carries no
// per-instance state — the template reads the current lineup straight off the
// injected subject, so the first (page-load) render and every pushed
// re-render draw from the same source.
type Roster struct {
	// HydroID marks the component interactive so pushes can target it.
	HydroID string
	// Board is the app-lifetime subject feeding the lineup.
	Board *liquid.BehaviorSubject[[]Player] `inject:""`
}

// Selector returns the custom element tag for the component.
func (r *Roster) Selector() string { return "app-roster" }

// Players is the template's *goFor source: the board's current snapshot.
func (r *Roster) Players() []Player { return r.Board.Value() }

// Total is the summed projected points across the lineup, formatted for the
// footer — recomputed on every render from the latest snapshot.
func (r *Roster) Total() string {
	var sum float64
	for _, p := range r.Board.Value() {
		v, err := strconv.ParseFloat(p.Points, 64)
		if err != nil {
			continue
		}
		sum += v
	}
	return fmt.Sprintf("%.1f", sum)
}

// Subscriptions follows the board subject: each emission re-renders the rail
// (the render pulls the latest snapshot via Players), so apply is a no-op —
// the binding exists only to drive the push (D3/D20).
func (r *Roster) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(r.Board, func([]Player) {}),
	}
}
