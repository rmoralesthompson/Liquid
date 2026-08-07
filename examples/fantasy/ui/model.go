package ui

import (
	"fmt"
	"math"
)

// This file holds the display models for the league dashboard (the fantasy
// example's first page): the weekly head-to-head, the league standings, and the
// live news/stats ticker. Every field is preformatted on the server — the
// templates do no arithmetic. All names, managers, players, and clubs are
// FICTIONAL (see the IRON-CLAD rule in the main package); nothing here is a real
// person, athlete, or organisation.

// Side is one team in the weekly head-to-head.
type Side struct {
	Team    string // fantasy team name
	Manager string // fictional manager
	Record  string // e.g. "4-1"
	Score   string // live points, e.g. "78.4"
	Proj    string // projected final, e.g. "121.6"
	Top     string // one-line top-performer blurb
	IsYou   bool
}

// MakeSide formats a raw side into its display form.
func MakeSide(team, manager, record string, score, proj float64, top string, isYou bool) Side {
	return Side{
		Team:    team,
		Manager: manager,
		Record:  record,
		Score:   fmt.Sprintf("%.1f", score),
		Proj:    fmt.Sprintf("%.1f", proj),
		Top:     top,
		IsYou:   isYou,
	}
}

// MatchState is this week's head-to-head between your team and your opponent.
type MatchState struct {
	You     Side
	Opp     Side
	Clock   string // e.g. "Q3 · 7:42 left"
	WinPct  string // your win probability, integer percent, e.g. "63"
	WinBar  string // the same value as a CSS width, e.g. "63%"
	Leading bool   // are you ahead on the scoreboard right now
}

// MakeMatch assembles the head-to-head; winPct is your win probability (0–100)
// and leading is whether your live score is currently ahead.
func MakeMatch(you, opp Side, clock string, winPct int, leading bool) MatchState {
	return MatchState{
		You:     you,
		Opp:     opp,
		Clock:   clock,
		WinPct:  fmt.Sprintf("%d", winPct),
		WinBar:  fmt.Sprintf("%d%%", winPct),
		Leading: leading,
	}
}

// TeamStanding is one row in the league table.
type TeamStanding struct {
	Rank      string
	Name      string
	Manager   string
	Record    string
	PointsFor string
	RowClass  string // "row--you", "row--opp", or "" — keys the row highlight
	LiveClass string // "on" when the team is playing now (a pulsing dot), else "off"
}

// MakeStanding formats a raw standings row. rowClass highlights your team and
// this week's opponent; live marks a team playing right now.
func MakeStanding(rank int, name, manager, record string, pointsFor float64, rowClass string, live bool) TeamStanding {
	liveClass := "off"
	if live {
		liveClass = "on"
	}
	return TeamStanding{
		Rank:      fmt.Sprintf("%d", rank),
		Name:      name,
		Manager:   manager,
		Record:    record,
		PointsFor: fmt.Sprintf("%.1f", pointsFor),
		RowClass:  rowClass,
		LiveClass: liveClass,
	}
}

// MiniGame is one other head-to-head in the league this week (the "around the
// league" rail), with the current leader flagged and a home-team win
// probability the featured view reveals.
type MiniGame struct {
	Idx                  string // stable index — the data-idx a click carries to feature it
	Home, Away           string
	HomeScore, AwayScore string
	HomeClass, AwayClass string // "mini__side--lead" on the side currently ahead
	WinPct               string // the home team's win probability, 0–100, for the featured bar
}

// MakeMiniGame formats a raw other-matchup, marks the leading side, and derives
// a win probability from the score margin.
func MakeMiniGame(idx int, home, away string, homeScore, awayScore float64) MiniGame {
	hc, ac := "", "mini__side--lead"
	if homeScore >= awayScore {
		hc, ac = "mini__side--lead", ""
	}
	pct := 50 + (homeScore-awayScore)*1.6
	pct = math.Max(4, math.Min(96, pct))
	return MiniGame{
		Idx:       fmt.Sprintf("%d", idx),
		Home:      home,
		Away:      away,
		HomeScore: fmt.Sprintf("%.1f", homeScore),
		AwayScore: fmt.Sprintf("%.1f", awayScore),
		HomeClass: hc,
		AwayClass: ac,
		WinPct:    fmt.Sprintf("%d", int(math.Round(pct))),
	}
}

// TickerItem is one entry in the live news/stats rail.
type TickerItem struct {
	Kind      string // "NEWS", "STAT", or "FINAL"
	KindClass string // "news", "stat", "final" — keys the badge colour
	Team      string // fictional pro-team code, e.g. "APX"
	Text      string
}

// MakeTickerItem tags an item with its badge colour class.
func MakeTickerItem(kind, team, text string) TickerItem {
	class := "news"
	switch kind {
	case "STAT":
		class = "stat"
	case "FINAL":
		class = "final"
	}
	return TickerItem{Kind: kind, KindClass: class, Team: team, Text: text}
}
