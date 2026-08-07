package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Standings is the interactive league table, fed the live standings over SSE.
// Points-for tick up live for teams currently playing and the table re-sorts
// server-side, so the ladder reorders in place. A (click) segmented toggle
// filters between the full table and the top-six playoff picture, per session.
type Standings struct {
	// HydroID marks the table interactive so pushes can target it.
	HydroID string
	// Table is the app-lifetime subject carrying the ordered league table.
	Table *liquid.BehaviorSubject[[]TeamStanding] `inject:""`
	// Playoff is per-session view state toggled by (click): true shows only the
	// top-six playoff picture.
	Playoff bool
}

func (s *Standings) Selector() string { return "app-standings" }

// Teams is the template's *goFor source: the current ladder, trimmed to the
// top six when the playoff-picture view is on.
func (s *Standings) Teams() []TeamStanding {
	all := s.Table.Value()
	if s.Playoff && len(all) > 6 {
		return all[:6]
	}
	return all
}

// ShowFull and ShowPlayoff are the (click) actions behind the segmented toggle.
func (s *Standings) ShowFull()    { s.Playoff = false }
func (s *Standings) ShowPlayoff() { s.Playoff = true }

// FullClass and PlayoffClass mark the active segment.
func (s *Standings) FullClass() string {
	if s.Playoff {
		return ""
	}
	return "seg__btn--on"
}

func (s *Standings) PlayoffClass() string {
	if s.Playoff {
		return "seg__btn--on"
	}
	return ""
}

// Subscriptions drives the live push; the render pulls the latest ladder via
// Teams, so apply is a no-op.
func (s *Standings) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(s.Table, func([]TeamStanding) {})}
}
