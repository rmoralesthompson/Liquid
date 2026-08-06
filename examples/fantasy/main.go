// Command fantasy is a Liquid example: a fantasy-football starting lineup whose
// projected points stream live over SSE. It's a focused counterpart to the
// dashboard example — one interactive card (the roster) fed structured data
// (`[]ui.Player`) pushed from a shared subject, rendered in a gunmetal-and-neon
// theme with server-drawn SVG headshots (no client charting or JS framework).
//
// Build the templates, then run it:
//
//	go run ./cmd/liquid build examples/fantasy/ui
//	go run ./examples/fantasy
//
// or, with the dev loop (watch + rebuild + reload):
//
//	go run ./cmd/liquid dev examples/fantasy
//
// The components live in the ui subpackage — the directory liquid build
// compiles — while this package holds the wiring that needs the generated
// Template methods to exist.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/examples/fantasy/ui"
)

// starter is one raw lineup slot: the roster metadata plus the live projected
// points the feed walks. The display Player is derived from it each tick.
type starter struct {
	name, team, pos string
	number          int
	points          float64
}

// seedLineup is the starting roster — fixed so the first render (and the
// tests) are deterministic; the feed walks the projections once it starts.
// One slot per fantasy position: QB, two RB, three WR, TE, and a FLEX.
func seedLineup() []starter {
	return []starter{
		{"Patrick Mahomes", "KC", "QB", 15, 22.4},
		{"Christian McCaffrey", "SF", "RB", 23, 24.8},
		{"Saquon Barkley", "PHI", "RB", 26, 19.6},
		{"Tyreek Hill", "MIA", "WR", 10, 21.1},
		{"CeeDee Lamb", "DAL", "WR", 88, 18.9},
		{"Ja'Marr Chase", "CIN", "WR", 1, 20.3},
		{"Travis Kelce", "KC", "TE", 87, 13.7},
		{"Josh Allen", "BUF", "FLX", 17, 17.2},
	}
}

// playersOf renders the raw lineup into display Players.
func playersOf(ss []starter) []ui.Player {
	out := make([]ui.Player, len(ss))
	for i, s := range ss {
		out[i] = ui.MakePlayer(s.name, s.team, s.pos, s.number, s.points)
	}
	return out
}

// newApp wires the example: the shared board as a service, the interactive
// roster as a child component, and the single route. Tests build the same app
// around their own board.
func newApp(board *liquid.BehaviorSubject[[]ui.Player]) (*liquid.App, error) {
	app := liquid.New()
	if err := app.Provide(board); err != nil {
		return nil, fmt.Errorf("providing board: %w", err)
	}
	if err := app.Register(&ui.Roster{}); err != nil {
		return nil, fmt.Errorf("registering %s: %w", (&ui.Roster{}).Selector(), err)
	}
	if err := app.Route("/", &ui.Lineup{Week: "Week 5", Manager: "Thompson's Titans"}); err != nil {
		return nil, fmt.Errorf("routing /: %w", err)
	}
	return app, nil
}

// driveProjections random-walks each player's projected points once a second
// and republishes the lineup — the stand-in for a real live-scoring feed.
func driveProjections(ctx context.Context, board *liquid.BehaviorSubject[[]ui.Player], lineup []starter) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for i := range lineup {
				lineup[i].points += (rand.Float64()*2 - 1) * 0.4
				if lineup[i].points < 0 {
					lineup[i].points = 0
				}
			}
			board.Next(playersOf(lineup))
		}
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	lineup := seedLineup()
	board := liquid.NewBehaviorSubject(playersOf(lineup))
	app, err := newApp(board)
	if err != nil {
		logger.Error("wiring app", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go driveProjections(ctx, board, lineup)

	const addr = ":8080"
	logger.Info("fantasy lineup listening", "addr", "http://localhost"+addr)
	if err := http.ListenAndServe(addr, app); err != nil {
		logger.Error("serving", "err", err)
		os.Exit(1)
	}
}
