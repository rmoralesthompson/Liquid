//go:build liquiddev

package liquid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// This test runs only under the liquiddev build tag — WithDeterminism, the
// opt-in half of D28, exists only there (determinism_off_test.go pins that a
// production build cannot reach it). It exercises the real public seam
// end-to-end through the HTTP surface, black-box, the way a snapshot consumer
// (#D27) would.

// detCounter is a minimal interactive component: a hydro token is minted for
// it, so its render is non-deterministic unless the token source is pinned.
type detCounter struct {
	HydroID   string
	CSRFToken string
	Count     int
}

func (c *detCounter) Selector() string { return "app-det-counter" }
func (c *detCounter) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{ .Count }}</div>`
}
func (c *detCounter) Actions() []string { return []string{"Increment"} }
func (c *detCounter) Increment()        { c.Count++ }

// TestWithDeterminismRendersByteIdentically pins the D28 acceptance criterion
// through the public opt-in: two independently constructed
// New(WithDeterminism()) Apps serve the same component byte-identically —
// hydro token, CSRF token, and clock-derived output all reproduced.
func TestWithDeterminismRendersByteIdentically(t *testing.T) {
	render := func() string {
		app := liquid.New(liquid.WithDeterminism())
		if err := app.Route("/", &detCounter{}); err != nil {
			t.Fatalf("Route: %v", err)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200:\n%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if first, second := render(), render(); first != second {
		t.Fatalf("WithDeterminism render is not byte-identical\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
