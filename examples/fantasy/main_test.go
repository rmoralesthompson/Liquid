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
	app, err := newApp(services{
		board:  f.board,
		weekly: f.weekly,
		table:  f.table,
		feed:   f.ticker,
		slate:  f.slate,
		store:  buildStore(clubs),
	})
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
	// The ticker renders its seeded window twice — the second copy makes the
	// right-to-left marquee loop seamlessly.
	if items := strings.Count(page.Body, `class="tick tick--`); items != tickerWindow*2 {
		t.Errorf("ticker rendered %d tick copies, want %d (window x2 for the marquee)", items, tickerWindow*2)
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
	page := h.Get("/team/thunder-yaks")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /team = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	seeded := playersOf(seedLineup())
	if rows := strings.Count(page.Body, `class="player__pos `); rows != len(seeded) {
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
	h.Get("/team/thunder-yaks")

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
	page := h.Get("/team/thunder-yaks")
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

// --- clickable interactions -------------------------------------------------

// topScorer returns the highest-projected player — what the matchup surfaces as
// your top performer, wired to the live roster.
func topScorer(players []ui.Player) ui.Player {
	best := players[0]
	for _, p := range players[1:] {
		if parseScore(p.Points) > parseScore(best.Points) {
			best = p
		}
	}
	return best
}

func parseScore(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

func TestMatchupTopStarterIsWiredAndTogglable(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	// The headline top performer is your live roster's highest scorer.
	top := topScorer(playersOf(seedLineup()))
	if !strings.Contains(page.Body, top.Name) {
		t.Errorf("matchup does not surface your roster's top scorer %q", top.Name)
	}
	// Collapsed to start: no expanded starters list.
	if strings.Contains(page.Body, `class="starters"`) {
		t.Error("starters list shown before the toggle was clicked")
	}

	// Click the toggle: the (click) action reveals the starters list, still
	// drawn from the live lineup.
	patch := page.Fire("ToggleStarters", liquidtest.From("#matchup"))
	if patch.Code != http.StatusOK {
		t.Fatalf("Fire ToggleStarters = %d", patch.Code)
	}
	if !strings.Contains(patch.Envelope.Patch, `class="starters"`) {
		t.Errorf("toggle did not reveal the starters list:\n%s", patch.Envelope.Patch)
	}
	if !strings.Contains(patch.Envelope.Patch, top.Name) {
		t.Errorf("expanded starters missing your top scorer %q", top.Name)
	}
}

func TestStandingsPlayoffToggle(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	// Full table: every league team plus the header row.
	if rows := strings.Count(page.Body, `role="row"`); rows != len(clubNames)+1 {
		t.Fatalf("full table rows = %d, want %d", rows, len(clubNames)+1)
	}

	// Click "Top 6": the (click) action trims to the playoff picture.
	patch := page.Fire("ShowPlayoff", liquidtest.From("#standings"))
	if patch.Code != http.StatusOK {
		t.Fatalf("Fire ShowPlayoff = %d", patch.Code)
	}
	if rows := strings.Count(patch.Envelope.Patch, `role="row"`); rows != 6+1 {
		t.Errorf("playoff view rows = %d, want 7 (top 6 + header)", rows)
	}

	// Click "Full": back to the whole ladder.
	patch = page.Fire("ShowFull", liquidtest.From("#standings"))
	if rows := strings.Count(patch.Envelope.Patch, `role="row"`); rows != len(clubNames)+1 {
		t.Errorf("full view rows after toggling back = %d, want %d", rows, len(clubNames)+1)
	}
}

// featuredHome returns the home team of the featured (expanded) around-league
// game in a rendered fragment — the first team name inside the mini--featured
// block.
func featuredHome(html string) string {
	i := strings.Index(html, "mini mini--featured")
	if i < 0 {
		return ""
	}
	seg := html[i:]
	const marker = `mini__team">`
	j := strings.Index(seg, marker)
	if j < 0 {
		return ""
	}
	seg = seg[j+len(marker):]
	k := strings.IndexByte(seg, '<')
	if k < 0 {
		return ""
	}
	return strings.TrimSpace(seg[:k])
}

func TestAroundFeaturesGameFromClickPayload(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	clubs := seedLeague()

	// On load exactly one game is featured — the first (clubs[2] vs clubs[3]).
	if n := strings.Count(page.Body, "mini mini--featured"); n != 1 {
		t.Fatalf("featured games on load = %d, want 1", n)
	}
	if got, want := featuredHome(page.Body), clubs[2].name; got != want {
		t.Errorf("default featured home = %q, want %q", got, want)
	}

	// Click the last game — the (click) carries its data-idx as a typed
	// payload, so that game (clubs[8] vs clubs[9]) becomes featured.
	patch := page.Fire("Feature", liquidtest.From("#around"), liquidtest.Field("idx", "3"))
	if patch.Code != http.StatusOK {
		t.Fatalf("Fire Feature = %d", patch.Code)
	}
	if n := strings.Count(patch.Envelope.Patch, "mini mini--featured"); n != 1 {
		t.Errorf("featured games after click = %d, want 1", n)
	}
	if got, want := featuredHome(patch.Envelope.Patch), clubs[8].name; got != want {
		t.Errorf("featured home after clicking idx 3 = %q, want %q", got, want)
	}
}

func TestStandingsSearchFilters(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	// Type "neon": the (input) filters the ladder to matching teams only.
	patch := page.Fire("Search", liquidtest.From("#standings"), liquidtest.Field("value", "neon"))
	if patch.Code != http.StatusOK {
		t.Fatalf("Fire Search = %d", patch.Code)
	}
	if !strings.Contains(patch.Envelope.Patch, "Neon Comets") {
		t.Error("filtered standings dropped the matching team")
	}
	if strings.Contains(patch.Envelope.Patch, "Thunder Yaks") {
		t.Error("filtered standings still shows a non-matching team")
	}
	if rows := strings.Count(patch.Envelope.Patch, `role="row"`); rows != 1+1 {
		t.Errorf("filtered rows = %d, want 2 (one match + header)", rows)
	}
}

func TestLineupPageNavigatesBackToDashboard(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/team/thunder-yaks")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /team/thunder-yaks = %d, want 200", page.Code)
	}
	// The lineup page offers a link back to the dashboard.
	if !strings.Contains(page.Body, `href="/"`) {
		t.Error("lineup page has no link back to the dashboard")
	}
	// ...and the dashboard links out to team lineups.
	dash := h.Get("/")
	if !strings.Contains(dash.Body, `href="/team/`) {
		t.Error("dashboard has no link to a team lineup")
	}
}

func TestMatchupTilesLinkToLineups(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	// Your team's tile links to your lineup; the opponent's tile links to theirs.
	if !strings.Contains(page.Body, `tile-link" href="/team/thunder-yaks"`) {
		t.Error("the matchup 'you' tile does not link to /team/thunder-yaks")
	}
	if !strings.Contains(page.Body, `href="/team/neon-comets"`) {
		t.Error("the matchup opponent tile does not link to /team/neon-comets")
	}
	if !strings.Contains(page.Body, "tile-link--opp") {
		t.Error("the opponent tile is not rendered as a link")
	}
}

func TestStandingsRowsLinkToLineups(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	// Every standings team row is a link to that team's lineup.
	if rows := strings.Count(page.Body, `class="table__row table__row--link`); rows != len(clubNames) {
		t.Errorf("standings row links = %d, want %d (one per team)", rows, len(clubNames))
	}
	// A representative team's row targets its slug.
	if !strings.Contains(page.Body, `href="/team/velvet-hammers"`) {
		t.Error("standings row does not link to /team/velvet-hammers")
	}
}

func TestOpponentLineupRendersStaticRoster(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/team/neon-comets")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /team/neon-comets = %d, want 200", page.Code)
	}
	// The opponent's page names the team and shows this week's opponent (you).
	if !strings.Contains(page.Body, "Neon Comets") {
		t.Error("opponent lineup page missing the team name")
	}
	if !strings.Contains(page.Body, "Thunder Yaks") {
		t.Error("opponent lineup page missing this week's opponent (your team)")
	}
	// It renders a full seeded roster — one position badge per starter — and a
	// projected total, without the live board (that is your team's page only).
	if rows := strings.Count(page.Body, `class="player__pos `); rows != len(lineupShape) {
		t.Errorf("opponent roster rendered %d rows, want %d", rows, len(lineupShape))
	}
	if !strings.Contains(page.Body, "PROJ TOTAL") {
		t.Error("opponent lineup page missing the projected total")
	}
	if strings.Contains(page.Body, `id="roster"`) {
		t.Error("opponent page should render the static roster, not the live board")
	}
}

func TestUnknownTeamShowsNotFound(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/team/no-such-team")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /team/no-such-team = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body, "Team not found") {
		t.Error("unknown team slug did not render the not-found panel")
	}
}
