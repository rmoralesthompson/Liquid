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

// newHarness builds the real app around a test-owned board, so tests drive the
// projection feed deterministically instead of racing the once-a-second walk.
func newHarness(t *testing.T) (*liquidtest.Harness, *liquid.BehaviorSubject[[]ui.Player]) {
	t.Helper()
	board := liquid.NewBehaviorSubject(playersOf(seedLineup()))
	app, err := newApp(board)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return liquidtest.New(t, app), board
}

func TestRosterRendersSeededLineup(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	if page.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	// The roster renders its seeded lineup straight off the injected board —
	// no [input] and no OnInit (children skip it). Names and teams are
	// randomly generated fictional values (never real), so assert on the
	// deterministic seed rather than any hardcoded name.
	seeded := playersOf(seedLineup())
	if rows := strings.Count(page.Body, `class="player__pos"`); rows != len(seeded) {
		t.Errorf("rendered %d player rows, want %d", rows, len(seeded))
	}
	if !strings.Contains(page.Body, seeded[0].Name) {
		t.Errorf("page missing generated player name %q\n--- body ---\n%s", seeded[0].Name, page.Body)
	}
	if !strings.Contains(page.Body, "avatar--"+seeded[0].TeamClass) {
		t.Errorf("page missing team headshot class %q", "avatar--"+seeded[0].TeamClass)
	}
	// The footer total is summed on the server from the seeded projections.
	if got, want := page.Text("#lineup-total"), expectedTotal(t, seeded); got != want {
		t.Errorf(`Text("#lineup-total") = %q, want the seeded sum %q`, got, want)
	}
	if page.Text("h1") == "" {
		t.Error("page renders no h1 heading")
	}
}

func TestProjectionUpdateIsPushedOverSSE(t *testing.T) {
	h, board := newHarness(t)
	h.Get("/")

	stream := h.Stream()
	defer stream.Close()
	if stream.Code != http.StatusOK {
		t.Fatalf("Stream() = %d, want 200", stream.Code)
	}

	// Bump one projection by +10 and republish; the footer total moves with it.
	updated := seedLineup()
	updated[0].points += 10
	want := expectedTotal(t, playersOf(updated))
	board.Next(playersOf(updated))

	// Connecting the stream primes a current-state frame first; scan a few
	// pushes for the one carrying the updated roster.
	var seen []string
	for range 10 {
		push := stream.Next()
		if !strings.Contains(push.Patch, `id="roster"`) {
			continue
		}
		got := push.Text("#lineup-total")
		if got == want {
			return
		}
		seen = append(seen, got)
	}
	t.Fatalf(`no SSE push showed #lineup-total %q; saw %v`, want, seen)
}

func TestRosterRegionDeclaresAriaLive(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	// D21: the push-updated region itself must announce to assistive tech —
	// the innerHTML swap emits no announcement of its own.
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
		t.Fatalf("no #roster region in the page\n--- body ---\n%s", page.Body)
	}
}
