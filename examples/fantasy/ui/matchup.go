package ui

import (
	"sort"
	"strconv"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// Matchup is the interactive weekly head-to-head card, fed the live match state
// over SSE. It shows your team versus this week's opponent with live scores,
// projections, records, and a win-probability bar that all update in place. Its
// "top performer" is wired to your actual roster (the /team lineup): the
// highest-scoring starter, live. A (click) toggle expands the full top-four.
type Matchup struct {
	// HydroID marks the card interactive so pushes can target it.
	HydroID string
	// Match is the app-lifetime subject carrying this week's head-to-head.
	Match *liquid.BehaviorSubject[MatchState] `inject:""`
	// Board is your live starting lineup — the same feed the /team roster uses,
	// so the matchup's top performers reflect your real players.
	Board *liquid.BehaviorSubject[[]Player] `inject:""`
	// Expanded is per-session view state toggled by (click): whether the
	// top-starters list is open.
	Expanded bool
	// pulse flips on every match push so the score gets a fresh flash. It
	// alternates two class names (below) because a CSS animation only restarts
	// when its name changes across an Idiomorph patch.
	pulse bool
}

func (m *Matchup) Selector() string { return "app-matchup" }

// You and Opp expose the two sides; the template reads their fields directly.
func (m *Matchup) You() Side { return m.Match.Value().You }
func (m *Matchup) Opp() Side { return m.Match.Value().Opp }

// Clock, WinPct, and WinBar drive the game clock and the win-probability bar.
func (m *Matchup) Clock() string  { return m.Match.Value().Clock }
func (m *Matchup) WinPct() string { return m.Match.Value().WinPct }
func (m *Matchup) WinBar() string { return m.Match.Value().WinBar }

// Leading is true when your live score is currently ahead.
func (m *Matchup) Leading() bool { return m.Match.Value().Leading }

// points parses a player's formatted projection back to a number for ranking.
func points(p Player) float64 { v, _ := strconv.ParseFloat(p.Points, 64); return v }

// topStarters returns your roster's highest-scoring players — most first — off a
// copy of the live board so the shared snapshot is never sorted in place.
func (m *Matchup) topStarters(n int) []Player {
	players := append([]Player(nil), m.Board.Value()...)
	sort.SliceStable(players, func(i, j int) bool { return points(players[i]) > points(players[j]) })
	if len(players) > n {
		players = players[:n]
	}
	return players
}

// TopStarter is your single highest scorer — the headline top performer, wired
// to the live lineup.
func (m *Matchup) TopStarter() Player {
	top := m.topStarters(1)
	if len(top) == 0 {
		return Player{}
	}
	return top[0]
}

// Starters is the top-four list revealed when the toggle is open.
func (m *Matchup) Starters() []Player { return m.topStarters(4) }

// ToggleStarters is the (click) action that opens or closes the starters list.
func (m *Matchup) ToggleStarters() { m.Expanded = !m.Expanded }

// PulseClass alternates two identical flash animations so a score change
// re-triggers the highlight on each push.
func (m *Matchup) PulseClass() string {
	if m.pulse {
		return "is-bumped-a"
	}
	return "is-bumped-b"
}

// Subscriptions drives the live push on either feed — the head-to-head or your
// lineup — so scores and top performers both stay live. The match apply flips
// the pulse so a score change flashes; the board apply is a no-op (the render
// pulls the latest snapshots via the accessors).
func (m *Matchup) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(m.Match, func(MatchState) { m.pulse = !m.pulse }),
		liquid.Observe(m.Board, func([]Player) {}),
	}
}
