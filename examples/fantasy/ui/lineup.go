package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Lineup is the routed team page (/team/:id): a static shell (brand bar, theme,
// shared SVG defs) that shows one league team's full lineup. Your own team
// renders the live roster child (patched over SSE); every other team renders a
// static, seeded roster resolved from the injected store. The page holds no
// hydro state of its own — the resolved team is read once in OnInit.
type Lineup struct {
	// Slug is the :id route segment, bound before OnInit.
	Slug string `pathParam:"id"`
	// Store resolves a team's lineup by slug (app-lifetime, read-only).
	Store *TeamStore `inject:""`

	team  TeamLineup
	found bool
}

// Selector returns the custom element tag for the component.
func (l *Lineup) Selector() string { return "app-lineup" }

// OnInit resolves the team from the slug once, before render.
func (l *Lineup) OnInit(_ liquid.Ctx) error {
	l.team, l.found = l.Store.Get(l.Slug)
	return nil
}

// Found reports whether the slug matched a league team.
func (l *Lineup) Found() bool { return l.found }

// Missing is the negation of Found, for the not-found *goIf branch.
func (l *Lineup) Missing() bool { return !l.found }

// IsYou reports whether this is your team (render the live roster).
func (l *Lineup) IsYou() bool { return l.found && l.team.IsYou }

// NotYou reports whether this is another team (render the static roster).
func (l *Lineup) NotYou() bool { return l.found && !l.team.IsYou }

// Title is the hero heading: "My Lineup" for your team, else the team name.
func (l *Lineup) Title() string {
	if l.team.IsYou {
		return "My Lineup"
	}
	return l.team.Name
}

// Name, Manager, Record, Opponent, Total, and Players expose the resolved team
// to the template; Week labels the header.
func (l *Lineup) Name() string       { return l.team.Name }
func (l *Lineup) Manager() string    { return l.team.Manager }
func (l *Lineup) Record() string     { return l.team.Record }
func (l *Lineup) Opponent() string   { return l.team.Opponent }
func (l *Lineup) Total() string      { return l.team.Total }
func (l *Lineup) Players() []Player  { return l.team.Players }
func (l *Lineup) Week() string       { return "Week 5" }

// Head sets the document title (D22).
func (l *Lineup) Head() liquid.Head {
	title := "Gridiron Guild — lineup"
	if l.found {
		title = "Gridiron Guild — " + l.team.Name
	}
	return liquid.Head{
		Title: title,
		Meta:  []liquid.Meta{{Name: "color-scheme", Content: "dark"}},
	}
}
