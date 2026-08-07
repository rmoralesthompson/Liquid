package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Matchup is the interactive weekly head-to-head card, fed the live match state
// over SSE. It shows your team versus this week's opponent with live scores,
// projections, records, and a win-probability bar that all update in place.
type Matchup struct {
	// HydroID marks the card interactive so pushes can target it.
	HydroID string
	// Match is the app-lifetime subject carrying this week's head-to-head.
	Match *liquid.BehaviorSubject[MatchState] `inject:""`
}

func (m *Matchup) Selector() string { return "app-matchup" }

// You and Opp expose the two sides; the template reads their fields directly.
func (m *Matchup) You() Side { return m.Match.Value().You }
func (m *Matchup) Opp() Side { return m.Match.Value().Opp }

// Clock, WinPct, and WinBar drive the game clock and the win-probability bar.
func (m *Matchup) Clock() string  { return m.Match.Value().Clock }
func (m *Matchup) WinPct() string { return m.Match.Value().WinPct }
func (m *Matchup) WinBar() string { return m.Match.Value().WinBar }

// Leading is true when your live score is currently ahead (styles the crown).
func (m *Matchup) Leading() bool { return m.Match.Value().Leading }

// Subscriptions drives the live push; the render pulls the latest snapshot via
// the accessors above, so apply is a no-op.
func (m *Matchup) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(m.Match, func(MatchState) {})}
}
