package main

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/html"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// newHarness builds the real app around a test-owned metric subject, so
// tests drive the feed deterministically instead of racing the ticker.
func newHarness(t *testing.T) (*liquidtest.Harness, *liquid.BehaviorSubject[int]) {
	t.Helper()
	requests := liquid.NewBehaviorSubject(42)
	market := liquid.NewBehaviorSubject(quotesOf(seedAssets()))
	series := liquid.NewBehaviorSubject(seedSeries())
	app, err := newApp(requests, market, series)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return liquidtest.New(t, app), requests
}

func TestDashboardRendersAllFiveCards(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	if page.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	if got := page.Text("#count"); got != "0" {
		t.Errorf(`counter Text("#count") = %q, want "0"`, got)
	}
	if got := page.Text("#reading"); got != "42" {
		t.Errorf(`metric Text("#reading") = %q, want the subject's current value "42"`, got)
	}
	if got := page.Text("#stat-value"); got != "12" {
		t.Errorf(`stat card Text("#stat-value") = %q, want the [value] input "12"`, got)
	}
	if got := page.Text("#board-name"); got != "Production" {
		t.Errorf(`renamer Text("#board-name") = %q, want the [name] input "Production"`, got)
	}
	if page.Text("h1") == "" {
		t.Error("page renders no h1 heading")
	}
}

func TestTickerAndChartRenderSeededData(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	// The ticker renders its seeded book straight off the injected subject —
	// no [input] and no OnInit (children skip it).
	if !strings.Contains(page.Body, "BTC") {
		t.Errorf("ticker did not render the seeded BTC quote\n--- body ---\n%s", page.Body)
	}
	// The chart's last value is a server-formatted USD figure, and its SVG
	// path is generated on the server.
	if got := page.Text("#chart-last"); !strings.HasPrefix(got, "$") {
		t.Errorf(`chart Text("#chart-last") = %q, want a "$"-prefixed value`, got)
	}
	if !strings.Contains(page.Body, `class="spark__line"`) {
		t.Error("chart did not render the server-side SVG sparkline")
	}
}

func TestCounterClickIncrementsAcrossRoundTrips(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	patch := page.Fire("Increment", liquidtest.From("#counter"))
	if patch.Code != http.StatusOK {
		t.Fatalf(`Fire("Increment") = %d, want 200`, patch.Code)
	}
	if got := patch.Text("#count"); got != "1" {
		t.Errorf(`patch Text("#count") = %q, want "1"`, got)
	}
	if got := page.Fire("Increment", liquidtest.From("#counter")).Text("#count"); got != "2" {
		t.Errorf(`second click Text("#count") = %q, want "2" — state must persist across events`, got)
	}
}

func TestMetricEmissionIsPushedOverSSE(t *testing.T) {
	h, requests := newHarness(t)
	h.Get("/")

	stream := h.Stream()
	defer stream.Close()
	if stream.Code != http.StatusOK {
		t.Fatalf("Stream() = %d, want 200", stream.Code)
	}

	requests.Next(97)

	// Several cards subscribe, so connecting the stream primes a current-state
	// push for each (metric, ticker, chart) before the requests emission
	// arrives; skip the frames that are not the metric's and scan a few pushes.
	var seen []string
	for range 10 {
		push := stream.Next()
		if !strings.Contains(push.Patch, `id="reading"`) {
			continue // a ticker/chart prime frame, not the metric card
		}
		got := push.Text("#reading")
		if got == "97" {
			return
		}
		seen = append(seen, got)
	}
	t.Fatalf(`no SSE push showed #reading "97"; saw %v`, seen)
}

func TestMetricRegionDeclaresAriaLive(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")
	// The D21 checklist: the push-updated region itself must announce to
	// assistive tech — the swap emits no announcement of its own, and
	// aria-live anywhere else on the page would not cover it.
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
			if id == "metric" {
				found = true
				if live != "polite" {
					t.Errorf(`#metric aria-live = %q, want "polite"`, live)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if !found {
		t.Fatalf("no #metric region in the page\n--- body ---\n%s", page.Body)
	}
}

func TestStatCardTogglesItsOwnPinState(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	if strings.Contains(page.Body, "Pinned to the top") {
		t.Fatal("stat card renders as pinned before any toggle")
	}
	patch := page.Fire("TogglePin", liquidtest.From("#stat-card"))
	if patch.Code != http.StatusOK {
		t.Fatalf(`Fire("TogglePin") = %d, want 200`, patch.Code)
	}
	if got := patch.Text("#pin-state"); got != "Pinned to the top of your reports." {
		t.Errorf(`patch Text("#pin-state") = %q, want the pinned banner`, got)
	}
	// The [input]-fed fields must survive the patch: the child re-renders
	// from its live instance, not a fresh copy of the prototype.
	if got := patch.Text("#stat-value"); got != "12" {
		t.Errorf(`patch Text("#stat-value") = %q, want "12" — [input] state lost on re-render`, got)
	}
}

func TestRenameSubmitUpdatesTheBoardName(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	patch := page.Fire("Rename", liquidtest.From("#renamer"), liquidtest.Field("name", "Staging"))
	if patch.Code != http.StatusOK {
		t.Fatalf(`Fire("Rename") = %d, want 200`, patch.Code)
	}
	if got := patch.Text("#board-name"); got != "Staging" {
		t.Errorf(`patch Text("#board-name") = %q, want "Staging"`, got)
	}
}

func TestRenameWithForgedCSRFTokenIsRefused(t *testing.T) {
	h, _ := newHarness(t)
	page := h.Get("/")

	patch := page.Fire("Rename", liquidtest.From("#renamer"),
		liquidtest.Field("name", "Evil"), liquidtest.CSRF("forged-token"))
	if patch.Code != http.StatusForbidden {
		t.Fatalf(`Fire("Rename") with a forged CSRF token = %d, want 403`, patch.Code)
	}
	// The refused event must not have reached the handler: a blank rename
	// re-renders the same live instance without changing its name.
	if got := page.Fire("Rename", liquidtest.From("#renamer")).Text("#board-name"); got != "Production" {
		t.Errorf(`live instance Text("#board-name") = %q, want "Production" — the forged event reached the handler`, got)
	}
}

func TestAdminRouteGuard(t *testing.T) {
	h, _ := newHarness(t)

	if got := h.Get("/admin?key=" + adminKey); got.Code != http.StatusOK {
		t.Errorf("GET /admin with the right key = %d, want 200", got.Code)
	} else if got.Text("h1") != "Admin area" {
		t.Errorf(`admin page Text("h1") = %q, want "Admin area"`, got.Text("h1"))
	}
	if got := h.Get("/admin"); got.Code != http.StatusFound {
		t.Errorf("GET /admin with no key = %d, want 302 redirect to the dashboard", got.Code)
	}
	if got := h.Get("/admin?key=wrong"); got.Code != http.StatusForbidden {
		t.Errorf("GET /admin with a wrong key = %d, want 403", got.Code)
	}
}
