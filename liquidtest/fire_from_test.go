package liquidtest_test

import (
	"net/http"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// pinger and ponger are two interactive children on one page; Fire's
// default first-hydro-id targeting can only ever reach pinger.
type pinger struct {
	HydroID string
	Count   int
}

func (p *pinger) Selector() string { return "app-pinger" }

func (p *pinger) Template() string {
	return `<div id="pinger" data-hydro-id="{{ .HydroID }}"><span id="ping-count">{{ .Count }}</span><button data-liquid-action="Ping">ping</button></div>`
}

// Actions is the compiled allowlist a real build would generate.
func (p *pinger) Actions() []string { return []string{"Ping"} }

// Ping handles the pinger's button.
func (p *pinger) Ping() { p.Count++ }

type ponger struct {
	HydroID string
	Count   int
}

func (p *ponger) Selector() string { return "app-ponger" }

func (p *ponger) Template() string {
	return `<div id="ponger" data-hydro-id="{{ .HydroID }}"><span id="pong-count">{{ .Count }}</span><button data-liquid-action="Pong">pong</button></div>`
}

// Actions is the compiled allowlist a real build would generate.
func (p *ponger) Actions() []string { return []string{"Pong"} }

// Pong handles the ponger's button.
func (p *ponger) Pong() { p.Count++ }

// twoCardPage statically hosts both children, like the D17 dashboard hosts
// its cards.
type twoCardPage struct{}

func (t *twoCardPage) Selector() string { return "app-two-cards" }

func (t *twoCardPage) Template() string {
	return `<main>{{liquidChild "app-pinger"}}{{liquidChild "app-ponger"}}</main>`
}

func newTwoCardHarness(t *testing.T) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Register(&pinger{}); err != nil {
		t.Fatalf("Register(pinger): %v", err)
	}
	if err := app.Register(&ponger{}); err != nil {
		t.Fatalf("Register(ponger): %v", err)
	}
	if err := app.Route("/", &twoCardPage{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestFireFromTargetsTheSelectedComponent(t *testing.T) {
	h := newTwoCardHarness(t)
	page := h.Get("/")

	patch := page.Fire("Pong", liquidtest.From("#ponger"))
	if patch.Code != http.StatusOK {
		t.Fatalf(`Fire("Pong", From("#ponger")) = %d, want 200`, patch.Code)
	}
	if got := patch.Text("#pong-count"); got != "1" {
		t.Errorf(`patch Text("#pong-count") = %q, want "1"`, got)
	}
}

func TestFireWithoutFromReachesOnlyTheFirstComponent(t *testing.T) {
	h := newTwoCardHarness(t)
	page := h.Get("/")

	// "Pong" is not on the first component's allowlist, so the default
	// first-hydro-id targeting must refuse it — the reason From exists.
	if patch := page.Fire("Pong"); patch.Code != http.StatusNotFound {
		t.Errorf(`Fire("Pong") without From = %d, want 404`, patch.Code)
	}
}
