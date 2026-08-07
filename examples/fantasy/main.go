// Command fantasy is a Liquid example: a live fantasy-football league dashboard.
// The first page shows your weekly head-to-head, the league standings, and a
// gameday news-and-stats ticker along the bottom — every score, ladder, and
// ticker item streaming in over SSE from server-side feeds, with no client
// charting or JS framework. A second page (/team) is the interactive starting
// lineup. It's a fuller counterpart to the dashboard example.
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
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/examples/fantasy/ui"
)

// ---------------------------------------------------------------------------
// IRON-CLAD RULE: every player, manager, club, and league in this demo is
// FICTIONAL. Never use the name or likeness of a real person (athlete or
// otherwise) or a real organisation. Names, clubs, and teams are assembled at
// random from the invented token pools below; any resemblance to a real person
// or club is coincidental. Do not replace them with real ones.
// ---------------------------------------------------------------------------

// firstNames and lastNames are invented tokens the generator combines into
// fictional player and manager names. None is a real person.
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

// team is a fictional pro franchise the players suit up for: a short code (also
// the stylesheet's colour key, lowercased -> .avatar--irn), a display colour,
// and a full name the ticker uses. All invented.
type team struct {
	code, color, name string
}

var teams = []team{
	{"IRN", "#5b6470", "Ironforge Anvils"},
	{"APX", "#c8102e", "Apex Predators"},
	{"BLZ", "#d64309", "Brushfire Blaze"},
	{"FRS", "#0e8a92", "Frostpeak Foxes"},
	{"NOV", "#7c3aed", "Nova Comets"},
	{"HVK", "#17335e", "Harbor Vikings"},
	{"RGE", "#9a1b30", "Rogue Riders"},
	{"RFT", "#2563eb", "Rift Raiders"},
}

// ---- the starting lineup (feeds the /team page) ----------------------------

// starter is one raw lineup slot: roster metadata plus live projected points.
type starter struct {
	name, team, pos string
	number          int
	points          float64
}

var lineupShape = []struct {
	pos  string
	base float64
}{
	{"QB", 22.5}, {"RB", 24.0}, {"RB", 19.5}, {"WR", 21.0},
	{"WR", 19.0}, {"WR", 20.0}, {"TE", 14.0}, {"FLX", 17.0},
}

// seedLineup builds the starting roster from randomly generated fictional names
// and teams (see the IRON-CLAD RULE). It draws from a fixed PRNG seed so the
// roster is deterministic — the first paint and tests are stable — while still
// being assembled at random, never hand-picked.
func seedLineup() []starter {
	r := rand.New(rand.NewPCG(1487, 9973))
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

func playersOf(ss []starter) []ui.Player {
	out := make([]ui.Player, len(ss))
	for i, s := range ss {
		out[i] = ui.MakePlayer(s.name, s.team, s.pos, s.number, s.points)
	}
	return out
}

// ---- the league (feeds the dashboard's standings + matchup) ----------------

// clubNames are invented fantasy-team names. Index 0 is your team.
var clubNames = []string{
	"Thunder Yaks", "Neon Comets", "Iron Wombats", "Velvet Hammers", "Granite Foxes",
	"Cobalt Krakens", "Static Mongoose", "Midnight Herons", "Crimson Badgers", "Aurora Bison",
}

// club is one fantasy team in your league: a fictional name and manager, a
// season record, live points-for, and flags for your team / this week's
// opponent / currently playing.
type club struct {
	name, manager      string
	wins, losses       int
	points             float64
	isYou, isOpp, live bool
}

// seedLeague assembles the ten-team league with fictional managers drawn from a
// fixed seed (deterministic first paint). Index 0 is you; index 1 is this
// week's opponent.
func seedLeague() []club {
	r := rand.New(rand.NewPCG(4242, 1013))
	clubs := make([]club, len(clubNames))
	for i, n := range clubNames {
		wins := r.IntN(5) // 0..4, over the first four weeks
		clubs[i] = club{
			name:    n,
			manager: firstNames[r.IntN(len(firstNames))] + " " + lastNames[r.IntN(len(lastNames))],
			wins:    wins,
			losses:  4 - wins,
			points:  380 + r.Float64()*190,
			live:    r.Float64() < 0.6,
		}
	}
	clubs[0].isYou, clubs[0].manager, clubs[0].live = true, "You", true
	clubs[1].isOpp, clubs[1].live = true, true
	return clubs
}

func record(c club) string { return fmt.Sprintf("%d-%d", c.wins, c.losses) }

// standingsOf sorts the league (wins, then points-for) and renders the ladder,
// highlighting your team and this week's opponent.
func standingsOf(clubs []club) []ui.TeamStanding {
	sorted := make([]club, len(clubs))
	copy(sorted, clubs)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].wins != sorted[j].wins {
			return sorted[i].wins > sorted[j].wins
		}
		return sorted[i].points > sorted[j].points
	})
	out := make([]ui.TeamStanding, len(sorted))
	for i, c := range sorted {
		row := ""
		switch {
		case c.isYou:
			row = "table__row--you"
		case c.isOpp:
			row = "table__row--opp"
		}
		out[i] = ui.MakeStanding(i+1, c.name, "mgr. "+c.manager, record(c), c.points, row, c.live)
	}
	return out
}

// match is the raw live state of your weekly head-to-head.
type match struct {
	youScore, oppScore float64
	youProj, oppProj   float64
	youTop, oppTop     string
	clock              string
}

// topLine returns a fictional "P. Lastname — line" top-performer blurb.
func topLine(r *rand.Rand) string {
	p := firstNames[r.IntN(len(firstNames))][:1] + ". " + lastNames[r.IntN(len(lastNames))]
	return fmt.Sprintf("%s %d pts", p, 14+r.IntN(22))
}

func seedMatch() match {
	r := rand.New(rand.NewPCG(77, 2029))
	return match{
		youScore: 58 + r.Float64()*22,
		oppScore: 54 + r.Float64()*22,
		youProj:  116 + r.Float64()*20,
		oppProj:  112 + r.Float64()*20,
		youTop:   topLine(r),
		oppTop:   topLine(r),
		clock:    "Q3 - 7:42 left",
	}
}

// matchOf renders the raw match into the display MatchState, deriving the win
// probability from the projected margin.
func matchOf(m match, clubs []club) ui.MatchState {
	you := ui.MakeSide(clubs[0].name, "You", record(clubs[0]), m.youScore, m.youProj, m.youTop, true)
	opp := ui.MakeSide(clubs[1].name, "mgr. "+clubs[1].manager, record(clubs[1]), m.oppScore, m.oppProj, m.oppTop, false)
	pct := int(math.Round(50 + (m.youProj-m.oppProj)*1.3))
	if pct < 4 {
		pct = 4
	}
	if pct > 96 {
		pct = 96
	}
	return ui.MakeMatch(you, opp, m.clock, pct, m.youScore >= m.oppScore)
}

// ---- the gameday ticker ----------------------------------------------------

var ailments = []string{"ankle", "hamstring", "shoulder", "concussion protocol", "illness", "back tightness"}
var weathers = []string{"20 mph gusts", "steady rain", "clear and cold", "field-goal winds", "light snow expected"}

func abbrev(r *rand.Rand) string {
	return firstNames[r.IntN(len(firstNames))][:1] + ". " + lastNames[r.IntN(len(lastNames))]
}

// nextTick fabricates one fictional gameday item.
func nextTick(r *rand.Rand) ui.TickerItem {
	t := teams[r.IntN(len(teams))]
	switch r.IntN(6) {
	case 0:
		return ui.MakeTickerItem("STAT", t.code, fmt.Sprintf("%s — %d rush yds, %d TD", abbrev(r), 40+r.IntN(120), 1+r.IntN(3)))
	case 1:
		return ui.MakeTickerItem("STAT", t.code, fmt.Sprintf("%s — %d rec, %d yds", abbrev(r), 3+r.IntN(9), 40+r.IntN(110)))
	case 2:
		return ui.MakeTickerItem("STAT", t.code, fmt.Sprintf("%s — %d/%d, %d yds, %d TD", abbrev(r), 14+r.IntN(12), 28+r.IntN(10), 190+r.IntN(160), r.IntN(4)))
	case 3:
		return ui.MakeTickerItem("NEWS", t.code, fmt.Sprintf("%s questionable to return — %s", abbrev(r), ailments[r.IntN(len(ailments))]))
	case 4:
		return ui.MakeTickerItem("NEWS", t.code, fmt.Sprintf("%s: %s", t.name, weathers[r.IntN(len(weathers))]))
	default:
		u := teams[r.IntN(len(teams))]
		return ui.MakeTickerItem("FINAL", t.code, fmt.Sprintf("%s %d, %s %d", t.name, 13+r.IntN(24), u.name, 10+r.IntN(24)))
	}
}

const tickerWindow = 14

// seedTicker fills the initial rolling window deterministically.
func seedTicker() []ui.TickerItem {
	r := rand.New(rand.NewPCG(555, 8081))
	out := make([]ui.TickerItem, tickerWindow)
	for i := range out {
		out[i] = nextTick(r)
	}
	return out
}

// ---- the "around the league" slate -----------------------------------------

// slatePairs are the other head-to-heads this week (club indices), excluding
// your team (0) and your opponent (1).
var slatePairs = [][2]int{{2, 3}, {4, 5}, {6, 7}, {8, 9}}

// seedSlate sets the other matchups' live scores from a fixed seed.
func seedSlate() [][2]float64 {
	r := rand.New(rand.NewPCG(31, 71))
	s := make([][2]float64, len(slatePairs))
	for i := range s {
		s[i] = [2]float64{58 + r.Float64()*44, 58 + r.Float64()*44}
	}
	return s
}

// gamesOf renders the other matchups against the current club names + scores.
func gamesOf(clubs []club, scores [][2]float64) []ui.MiniGame {
	out := make([]ui.MiniGame, len(slatePairs))
	for i, p := range slatePairs {
		out[i] = ui.MakeMiniGame(clubs[p[0]].name, clubs[p[1]].name, scores[i][0], scores[i][1])
	}
	return out
}

// ---- wiring ----------------------------------------------------------------

// newApp wires the example: the four live feeds as injectable services, the
// interactive cards as child components, and the two routes. Tests build the
// same app around their own subjects.
func newApp(
	board *liquid.BehaviorSubject[[]ui.Player],
	weekly *liquid.BehaviorSubject[ui.MatchState],
	table *liquid.BehaviorSubject[[]ui.TeamStanding],
	feed *liquid.BehaviorSubject[[]ui.TickerItem],
	slate *liquid.BehaviorSubject[[]ui.MiniGame],
) (*liquid.App, error) {
	app := liquid.New()
	for _, svc := range []any{board, weekly, table, feed, slate} {
		if err := app.Provide(svc); err != nil {
			return nil, fmt.Errorf("providing %T: %w", svc, err)
		}
	}
	for _, child := range []liquid.Component{&ui.Roster{}, &ui.Matchup{}, &ui.Standings{}, &ui.Ticker{}, &ui.Around{}} {
		if err := app.Register(child); err != nil {
			return nil, fmt.Errorf("registering %s: %w", child.Selector(), err)
		}
	}
	if err := app.Route("/", &ui.League{Name: "The Gridiron Guild", Week: "Week 5", Team: "Thunder Yaks", Record: "3-1", Rank: "3"}); err != nil {
		return nil, fmt.Errorf("routing /: %w", err)
	}
	if err := app.Route("/team", &ui.Lineup{Week: "Week 5", Manager: "Thunder Yaks"}); err != nil {
		return nil, fmt.Errorf("routing /team: %w", err)
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

// driveScores walks the live matchup and the live-playing clubs' points-for,
// republishing both the matchup and the re-sorted standings each tick.
func driveScores(ctx context.Context, weekly *liquid.BehaviorSubject[ui.MatchState], table *liquid.BehaviorSubject[[]ui.TeamStanding], clubs []club, m *match) {
	tick := time.NewTicker(1200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.youScore += rand.Float64() * 1.6
			m.oppScore += rand.Float64() * 1.6
			m.youProj += (rand.Float64()*2 - 1) * 0.5
			m.oppProj += (rand.Float64()*2 - 1) * 0.5
			for i := range clubs {
				if clubs[i].live {
					clubs[i].points += rand.Float64() * 1.4
				}
			}
			weekly.Next(matchOf(*m, clubs))
			table.Next(standingsOf(clubs))
		}
	}
}

// driveSlate walks the other matchups' live scores and republishes the slate.
func driveSlate(ctx context.Context, slate *liquid.BehaviorSubject[[]ui.MiniGame], clubs []club, scores [][2]float64) {
	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for i := range scores {
				scores[i][0] += rand.Float64() * 1.3
				scores[i][1] += rand.Float64() * 1.3
			}
			slate.Next(gamesOf(clubs, scores))
		}
	}
}

// driveTicker prepends a fresh fictional item to the rolling window every so
// often and republishes — the stand-in for a live news wire.
func driveTicker(ctx context.Context, feed *liquid.BehaviorSubject[[]ui.TickerItem], window []ui.TickerItem) {
	tick := time.NewTicker(2400 * time.Millisecond)
	defer tick.Stop()
	r := rand.New(rand.NewPCG(uint64(len(window)), 424242))
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			window = append([]ui.TickerItem{nextTick(r)}, window...)
			if len(window) > tickerWindow {
				window = window[:tickerWindow]
			}
			feed.Next(window)
		}
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	lineup := seedLineup()
	clubs := seedLeague()
	m := seedMatch()
	feed := seedTicker()
	scores := seedSlate()

	board := liquid.NewBehaviorSubject(playersOf(lineup))
	weekly := liquid.NewBehaviorSubject(matchOf(m, clubs))
	table := liquid.NewBehaviorSubject(standingsOf(clubs))
	ticker := liquid.NewBehaviorSubject(feed)
	slate := liquid.NewBehaviorSubject(gamesOf(clubs, scores))

	app, err := newApp(board, weekly, table, ticker, slate)
	if err != nil {
		logger.Error("wiring app", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go driveProjections(ctx, board, lineup)
	go driveScores(ctx, weekly, table, clubs, &m)
	go driveTicker(ctx, ticker, feed)
	go driveSlate(ctx, slate, clubs, scores)

	const addr = ":8080"
	logger.Info("fantasy league listening", "addr", "http://localhost"+addr)
	// Serve applies production timeouts and drains live SSE streams on
	// SIGINT/SIGTERM, so a deploy does not sever sessions abruptly.
	if err := app.Serve(ctx, liquid.ServeConfig{Addr: addr}); err != nil {
		logger.Error("serving", "err", err)
		os.Exit(1)
	}
	logger.Info("fantasy league stopped")
}
