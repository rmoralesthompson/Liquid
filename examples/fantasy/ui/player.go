// Package ui holds the fantasy-football example's components — the directory
// liquid build compiles. Wiring that needs the generated Template methods
// lives in the parent main package, so a from-scratch build always type-checks.
package ui

import (
	"fmt"
	"strings"
)

// Player is one row in the starting lineup. Every field is preformatted on the
// server (templates do no arithmetic): Points is the display string, and
// TeamClass is the lowercased team code the stylesheet keys the headshot's
// team colour off (e.g. "kc" -> .avatar--kc).
type Player struct {
	Name      string
	Team      string
	Pos       string
	Number    string
	Points    string
	TeamClass string // lowercased team code — keys the headshot colour
	PosClass  string // lowercased position — keys the position-badge colour
	Bar       string // 0–100, this player's points as a share of the lineup's top scorer
}

// MakePlayer formats a raw projection into a display Player. The feed (in
// package main) owns the numbers; the ui package owns how they render. Bar is
// filled in by the feed once the whole lineup is known (it is relative to the
// top scorer).
func MakePlayer(name, team, pos string, number int, points float64) Player {
	return Player{
		Name:      name,
		Team:      team,
		Pos:       pos,
		Number:    fmt.Sprintf("#%d", number),
		Points:    fmt.Sprintf("%.1f", points),
		TeamClass: strings.ToLower(team),
		PosClass:  strings.ToLower(pos),
	}
}
