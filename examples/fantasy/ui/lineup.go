package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Lineup is the routed page: a static shell (brand bar, theme, shared SVG
// defs) that composes the interactive roster. It never re-renders after the
// initial response — the roster child patches itself over SSE — so it holds no
// injected services of its own.
type Lineup struct {
	// Week labels the header (e.g. "Week 5").
	Week string
	// Manager is your fantasy team's name, shown in the header.
	Manager string
	// Opponent is this week's opponent, for the matchup breadcrumb.
	Opponent string
}

// Selector returns the custom element tag for the component.
func (l *Lineup) Selector() string { return "app-lineup" }

// Head sets the document title (D22).
func (l *Lineup) Head() liquid.Head {
	return liquid.Head{
		Title: "Gridiron Guild — my lineup",
		Meta:  []liquid.Meta{{Name: "color-scheme", Content: "dark"}},
	}
}
