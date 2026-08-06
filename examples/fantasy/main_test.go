package main

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/html"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/examples/fantasy/ui"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

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
	// no [input] and no OnInit (children skip it).
	for _, want := range []string{"Patrick Mahomes", "Travis Kelce", "avatar--kc", "avatar--phi"} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("page missing %q\n--- body ---\n%s", want, page.Body)
		}
	}
	// The footer total is summed on the server from the seeded projections.
	if got := page.Text("#lineup-total"); got != "158.0" {
		t.Errorf(`Text("#lineup-total") = %q, want the seeded sum "158.0"`, got)
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

	// Bump one projection by +10 and republish: the new total is 168.0.
	updated := seedLineup()
	updated[0].points += 10
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
		if got == "168.0" {
			return
		}
		seen = append(seen, got)
	}
	t.Fatalf(`no SSE push showed #lineup-total "168.0"; saw %v`, seen)
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
