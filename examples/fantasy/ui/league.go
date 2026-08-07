package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// League is the routed dashboard page — the fantasy example's first page. It
// owns the stylesheet and brand header and nests the three live cards: the
// weekly matchup, the league standings, and the gameday ticker.
type League struct {
	Name   string // league name, shown in the brand bar
	Week   string // e.g. "Week 5"
	Team   string // your fantasy team name
	Record string // your record, e.g. "3-2"
	Rank   string // your standing, e.g. "5th of 10"
}

func (l *League) Selector() string { return "app-league" }

func (l *League) Head() liquid.Head {
	return liquid.Head{
		Title: "Gridiron Guild — league dashboard",
		Meta:  []liquid.Meta{{Name: "color-scheme", Content: "dark"}},
	}
}
