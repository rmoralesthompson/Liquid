package main

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/examples/fantasy/ui"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// feeds bundles the five live subjects so a test can drive any of them
// deterministically instead of racing the once-a-second walks.
type feeds struct {
	board  *liquid.BehaviorSubject[[]ui.Player]
	weekly *liquid.BehaviorSubject[ui.MatchState]
	table  *liquid.BehaviorSubject[[]ui.TeamStanding]
	ticker *liquid.BehaviorSubject[[]ui.TickerItem]
	slate  *liquid.BehaviorSubject[[]ui.MiniGame]
}

// expectedTotal sums the players' formatted projections the same way
// Roster.Total does, so assertions never hardcode a figure tied to the
// randomly generated lineup.
func expectedTotal(t *testing.T, players []ui.Player) string {
	t.Helper()
	var sum float64
	for _, p := range players {
		v, err := strconv.ParseFloat(p.Points, 64)
		if err != nil {
			t.Fatalf("unparseable Points %q: %v", p.Points, err)
		}
		sum += v
	}
	return strconv.FormatFloat(sum, 'f', 1, 64)
}

// newHarness builds the real app around test-owned subjects, so tests drive the
// feeds deterministically instead of racing the live walks.
func newHarness(t *testing.T) (*liquidtest.Harness, feeds) {
	t.Helper()
	clubs := seedLeague()
	f := feeds{
		board:  liquid.NewBehaviorSubject(playersOf(seedLineup())),
		weekly: liquid.NewBehaviorSubject(matchOf(seedMatch(), clubs)),
		table:  liquid.NewBehaviorSubject(standingsOf(clubs)),
		ticker: liquid.NewBehaviorSubject(seedTicker()),
		slate:  liquid.NewBehaviorSubject(gamesOf(clubs, seedSlate())),
	}
	app, err := newApp(f.board, f.weekly, f.table, f.ticker, f.slate)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return liquidtest.New(t, app), f
}

// --- the league dashboard (the first page, "/") -----------------------------

func TestLeagueDashboardRendersLiveCards(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	if page.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	// All three live cards render on the first page.
	for _, id := range []string{`id="matchup"`, `id="standings"`, `id="ticker"`} {
		if !strings.Contains(page.Body, id) {
			t.Errorf("dashboard missing card %s", id)
		}
	}
	// Your team and this week's opponent both appear (fictional names).
	for _, want := range []string{"Thunder Yaks", "Neon Comets"} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	// The standings render one row per league team plus the header row.
	if rows := strings.Count(page.Body, `role="row"`); rows != len(clubNames)+1 {
		t.Errorf("standings rendered %d rows, want %d (teams + header)", rows, len(clubNames)+1)
	}
	// The ticker renders its seeded window of items.
	if items := strings.Count(page.Body, `class="tick tick--`); items != tickerWindow {
		t.Errorf("ticker rendered %d items, want %d", items, tickerWindow)
	}
}

func TestStandingsUpdateIsPushedOverSSE(t *testing.T) {
	h, f := newHarness(t)
	h.Get("/")

	stream := h.Stream()
	defer stream.Close()
	if stream.Code != http.StatusOK {
		t.Fatalf("Stream() = %d, want 200", stream.Code)
	}

	// Bump your team's points-for hard and republish the ladder; a standings
	// push must carry the new figure.
	clubs := seedLeague()
	clubs[0].points += 250
	want := standingsRowFor(standingsOf(clubs), "Thunder Yaks")
	f.table.Next(standingsOf(clubs))

	for range 12 {
		push := stream.Next()
		if strings.Contains(push.Patch, `id="standings"`) && strings.Contains(push.Patch, want) {
			return
		}
	}
	t.Fatalf("no standings SSE push carried points-for %q", want)
}

// standingsRowFor returns the PointsFor string of the named team in a ladder.
func standingsRowFor(table []ui.TeamStanding, name string) string {
	for _, r := range table {
		if r.Name == name {
			return r.PointsFor
		}
	}
	return ""
}

// --- the starting lineup (the /team page) -----------------------------------

func TestRosterRendersSeededLineup(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/team")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /team = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	seeded := playersOf(seedLineup())
	if rows := strings.Count(page.Body, `class="player__pos"`); rows != len(seeded) {
		t.Errorf("rendered %d player rows, want %d", rows, len(seeded))
	}
	if !strings.Contains(page.Body, seeded[0].Name) {
		t.Errorf("page missing generated player name %q", seeded[0].Name)
	}
	if got, want := page.Text("#lineup-total"), expectedTotal(t, seeded); got != want {
		t.Errorf(`Text("#lineup-total") = %q, want the seeded sum %q`, got, want)
	}
}

func TestProjectionUpdateIsPushedOverSSE(t *testing.T) {
	h, f := newHarness(t)
	h.Get("/team")

	stream := h.Stream()
	defer stream.Close()
	if stream.Code != http.StatusOK {
		t.Fatalf("Stream() = %d, want 200", stream.Code)
	}

	updated := seedLineup()
	updated[0].points += 10
	want := expectedTotal(t, playersOf(updated))
	f.board.Next(playersOf(updated))

	var seen []string
	for range 10 {
		push := stream.Next()
		if !strings.Contains(push.Patch, `id="roster"`) {
			continue
		}
		if got := push.Text("#lineup-total"); got == want {
			return
		} else {
			seen = append(seen, got)
		}
	}
	t.Fatalf(`no SSE push showed #lineup-total %q; saw %v`, want, seen)
}

func TestRosterRegionDeclaresAriaLive(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/team")
	doc, err := html.Parse(strings.NewReader(page.Body))
	if err != nil {
		t.Fatalf("parsing page: %v", err)
	}
	var found bool
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			var id, live string
			for _, a := range n.Attr {
				switch a.Key {
				case "id":
					id = a.Val
				case "aria-live":
					live = a.Val
				}
			}
			if id == "roster" {
				found = true
				if live != "polite" {
					t.Errorf(`#roster aria-live = %q, want "polite"`, live)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if !found {
		t.Fatalf("no #roster region in the page")
	}
}
