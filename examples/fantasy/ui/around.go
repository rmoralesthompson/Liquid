package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Around is the interactive "around the league" card: the week's other
// head-to-heads, their live scores streaming in over SSE alongside your own.
type Around struct {
	// HydroID marks the card interactive so pushes can target it.
	HydroID string
	// Slate is the app-lifetime subject carrying the other live matchups.
	Slate *liquid.BehaviorSubject[[]MiniGame] `inject:""`
}

func (a *Around) Selector() string { return "app-around" }

// Games is the template's *goFor source: the current slate of other matchups.
func (a *Around) Games() []MiniGame { return a.Slate.Value() }

// Subscriptions drives the live push; the render pulls the latest slate via
// Games, so apply is a no-op.
func (a *Around) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(a.Slate, func([]MiniGame) {})}
}
