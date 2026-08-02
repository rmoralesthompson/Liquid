package liquidtest_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// counter is the canonical interactive component, hand-written the way
// liquid build would emit it.
type counter struct {
	HydroID string
	Count   int
}

func (c *counter) Selector() string { return "app-counter" }

func (c *counter) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="count" class="value">{{ .Count }}</span><button data-liquid-action="Increment">+1</button></div>`
}

// Increment handles the +1 button.
func (c *counter) Increment() { c.Count++ }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *counter) Actions() []string { return []string{"Increment"} }

func newCounterHarness(t *testing.T) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Route("/", &counter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestHarnessRendersAndQueriesAComponent(t *testing.T) {
	h := newCounterHarness(t)

	page := h.Get("/")

	if got := page.Text("#count"); got != "0" {
		t.Errorf(`Text("#count") = %q, want "0"`, got)
	}
	if got := page.Text(".value"); got != "0" {
		t.Errorf(`Text(".value") = %q, want "0"`, got)
	}
	if got := page.Text("button"); got != "+1" {
		t.Errorf(`Text("button") = %q, want "+1"`, got)
	}
	if page.HydroID() == "" {
		t.Error("HydroID() is empty for an interactive page")
	}
}

func TestHarnessFiresAnActionAndExposesThePatch(t *testing.T) {
	h := newCounterHarness(t)
	page := h.Get("/")

	patch := page.Fire("Increment")

	if got := patch.Text("#count"); got != "1" {
		t.Errorf(`patch Text("#count") = %q, want "1"`, got)
	}
	if !strings.Contains(patch.Envelope.Patch, `data-hydro-id`) {
		t.Errorf("Envelope.Patch = %q, want the raw patch HTML", patch.Envelope.Patch)
	}
	if patch.Envelope.Redirect != "" {
		t.Errorf("Envelope.Redirect = %q, want empty for a patch response", patch.Envelope.Redirect)
	}

	// Firing again exercises the same live instance across the harness's
	// session continuity.
	if got := page.Fire("Increment").Text("#count"); got != "2" {
		t.Errorf(`second patch Text("#count") = %q, want "2"`, got)
	}
}

func TestHarnessExposesRefusedActionsViaCode(t *testing.T) {
	h := newCounterHarness(t)
	page := h.Get("/")

	patch := page.Fire("NotAllowlisted")

	if patch.Code != 404 {
		t.Errorf("Code = %d, want 404 for an action outside the allowlist", patch.Code)
	}
	if patch.Envelope.Patch != "" {
		t.Errorf("Envelope.Patch = %q, want empty for a refused action", patch.Envelope.Patch)
	}
}

func TestCtxConstructorBuildsAUsableCtxForHandTests(t *testing.T) {
	req := httptest.NewRequest("GET", "/dashboards/x?tab=alerts", nil)

	ctx := liquidtest.Ctx(req, map[string]string{"id": "d-42"})

	if got := ctx.Param("id"); got != "d-42" {
		t.Errorf(`Param("id") = %q, want "d-42"`, got)
	}
	if got := ctx.Query("tab"); got != "alerts" {
		t.Errorf(`Query("tab") = %q, want "alerts"`, got)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("embedded context is not usable: %v", err)
	}
}

func TestCtxConstructorToleratesANilRequest(t *testing.T) {
	ctx := liquidtest.Ctx(nil, nil)

	if got := ctx.Query("anything"); got != "" {
		t.Errorf(`Query on a nil-request Ctx = %q, want ""`, got)
	}
	if got := ctx.Param("anything"); got != "" {
		t.Errorf(`Param on an empty Ctx = %q, want ""`, got)
	}
}
