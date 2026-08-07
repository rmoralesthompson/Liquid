package ui

import (
	"strings"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// Standings is the interactive league table, fed the live standings over SSE.
// Points-for tick up live for teams currently playing and the table re-sorts
// server-side, so the ladder reorders in place. A (click) segmented toggle
// filters between the full table and the top-six playoff picture, and an
// (input) search box filters by team or manager — both per session.
type Standings struct {
	// HydroID marks the table interactive so pushes can target it.
	HydroID string
	// Table is the app-lifetime subject carrying the ordered league table.
	Table *liquid.BehaviorSubject[[]TeamStanding] `inject:""`
	// Playoff trims to the top-six playoff picture when on.
	Playoff bool
	// Query is the live search filter over team and manager names.
	Query string
}

func (s *Standings) Selector() string { return "app-standings" }

// Teams is the template's *goFor source: the current ladder, filtered by the
// search query and trimmed to the top six when the playoff view is on.
func (s *Standings) Teams() []TeamStanding {
	all := s.Table.Value()
	if q := strings.ToLower(strings.TrimSpace(s.Query)); q != "" {
		kept := make([]TeamStanding, 0, len(all))
		for _, t := range all {
			if strings.Contains(strings.ToLower(t.Name), q) || strings.Contains(strings.ToLower(t.Manager), q) {
				kept = append(kept, t)
			}
		}
		all = kept
	}
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

// searchInput is the (input) payload: the search box's current value.
type searchInput struct{ Value string }

// Search is the (input) action behind the search box (debounced by the runtime).
func (s *Standings) Search(in searchInput) { s.Query = in.Value }

// Subscriptions drives the live push; the render pulls the latest ladder via
// Teams, so apply is a no-op.
func (s *Standings) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(s.Table, func([]TeamStanding) {})}
}
