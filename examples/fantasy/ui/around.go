package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Around is the interactive "around the league" card: the week's other
// head-to-heads, their live scores streaming in over SSE. Clicking a game
// features it — the row expands to reveal a win-probability bar — via a typed
// (click) payload carrying the game's index.
type Around struct {
	// HydroID marks the card interactive so pushes can target it.
	HydroID string
	// Slate is the app-lifetime subject carrying the other live matchups.
	Slate *liquid.BehaviorSubject[[]MiniGame] `inject:""`
	// Featured is per-session view state: which game is expanded (defaults to
	// the first).
	Featured int
}

func (a *Around) Selector() string { return "app-around" }

// GameView decorates a MiniGame with whether it is the featured (expanded) row.
type GameView struct {
	MiniGame
	FeaturedClass string // "mini--featured" when expanded
}

// Games is the template's *goFor source: the current slate, each row tagged
// with whether it is the featured one.
func (a *Around) Games() []GameView {
	raw := a.Slate.Value()
	out := make([]GameView, len(raw))
	for i, g := range raw {
		fc := ""
		if i == a.Featured {
			fc = "mini--featured"
		}
		out[i] = GameView{MiniGame: g, FeaturedClass: fc}
	}
	return out
}

// gamePick is the typed (click) payload: the clicked game's index, bound from
// its data-idx attribute.
type gamePick struct{ Idx int }

// Feature is the (click) action behind each game row — it features (expands) the
// clicked game.
func (a *Around) Feature(p gamePick) { a.Featured = p.Idx }

// Subscriptions drives the live push; the render pulls the latest slate via
// Games, so apply is a no-op.
func (a *Around) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(a.Slate, func([]MiniGame) {})}
}
