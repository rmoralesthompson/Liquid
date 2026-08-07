package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Standings is the interactive league table, fed the live standings over SSE.
// Points-for tick up live for teams currently playing, and the table re-sorts
// itself server-side, so the ladder reorders in place.
type Standings struct {
	// HydroID marks the table interactive so pushes can target it.
	HydroID string
	// Table is the app-lifetime subject carrying the ordered league table.
	Table *liquid.BehaviorSubject[[]TeamStanding] `inject:""`
}

func (s *Standings) Selector() string { return "app-standings" }

// Teams is the template's *goFor source: the current league ladder.
func (s *Standings) Teams() []TeamStanding { return s.Table.Value() }

// Subscriptions drives the live push; the render pulls the latest ladder via
// Teams, so apply is a no-op.
func (s *Standings) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(s.Table, func([]TeamStanding) {})}
}
