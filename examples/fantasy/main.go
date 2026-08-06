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

// ---------------------------------------------------------------------------
// IRON-CLAD RULE: every player and team in this demo is FICTIONAL. Never use
// the name or likeness of a real person (athlete or otherwise) or a real
// organisation. Names and teams are assembled at random from the invented
// token pools below; any resemblance to a real person or club is coincidental.
// Do not replace them with real ones.
// ---------------------------------------------------------------------------

// firstNames and lastNames are invented tokens the generator combines into
// fictional player names. None is a real athlete.
var firstNames = []string{
	"Dax", "Cole", "Ronan", "Jace", "Kai", "Zane", "Trey", "Cade",
	"Nash", "Rhett", "Gunnar", "Wyatt", "Axel", "Rex", "Brock", "Knox",
	"Ryder", "Beckett", "Colt", "Tanner", "Slate", "Dane", "Cash", "Bo",
}

var lastNames = []string{
	"Voss", "Kessler", "Marek", "Sloan", "Rourke", "Calder", "Vance", "Hollis",
	"Thorne", "Brant", "Rennick", "Ashby", "Corwin", "Sable", "Faulk", "Grady",
	"Locke", "Merrow", "Halloran", "Kade", "Dorne", "Vale", "Ryker", "Stade",
}

// team is a fictional franchise: a short code (also the stylesheet's colour
// key, lowercased -> .avatar--irn) and its display colour. All invented.
type team struct {
	code, color string
}

var teams = []team{
	{"IRN", "#5b6470"}, {"APX", "#c8102e"}, {"BLZ", "#d64309"}, {"FRS", "#0e8a92"},
	{"NOV", "#7c3aed"}, {"HVK", "#17335e"}, {"RGE", "#9a1b30"}, {"RFT", "#2563eb"},
}

// lineupShape is the fixed starting-lineup: one slot per fantasy position with
// a baseline projection. Names, teams, and numbers are drawn at random per
// slot — QB, two RB, three WR, TE, and a FLEX.
var lineupShape = []struct {
	pos  string
	base float64
}{
	{"QB", 22.5}, {"RB", 24.0}, {"RB", 19.5}, {"WR", 21.0},
	{"WR", 19.0}, {"WR", 20.0}, {"TE", 14.0}, {"FLX", 17.0},
}

// seedLineup builds the starting roster with randomly generated fictional
// names and teams (see the IRON-CLAD RULE above). It draws from a fixed PRNG
// seed so the roster is deterministic — the first paint and the tests are
// stable — while still being assembled at random, never hand-picked.
func seedLineup() []starter {
	r := rand.New(rand.NewPCG(1487, 9973)) // fixed seed: deterministic lineup
	out := make([]starter, len(lineupShape))
	for i, slot := range lineupShape {
		name := firstNames[r.IntN(len(firstNames))] + " " + lastNames[r.IntN(len(lastNames))]
		out[i] = starter{
			name:   name,
			team:   teams[r.IntN(len(teams))].code,
			pos:    slot.pos,
			number: r.IntN(98) + 1,
			points: slot.base + (r.Float64()*2-1)*2.5,
		}
	}
	return out
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
