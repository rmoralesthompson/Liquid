//go:build !liquiddev

package liquid

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prodPage is a minimal static component for asserting the production
// surface.
type prodPage struct{}

func (p *prodPage) Selector() string { return "app-prod-page" }
func (p *prodPage) Template() string { return `<p>prod page</p>` }

// TestProductionBuildExcludesTheDevSurface pins the #12 acceptance criterion:
// a binary built without the liquiddev tag serves no dev script, injects no
// dev tag, and keeps /hydro-sse's session requirement even for ?dev=1.
func TestProductionBuildExcludesTheDevSurface(t *testing.T) {
	app := New()
	if err := app.Route("/", &prodPage{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), "dev.js") {
		t.Errorf("production shell must not reference the dev script, got:\n%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/liquid/dev.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /liquid/dev.js = %d, want 404 in a production build", rec.Code)
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hydro-sse?dev=1", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("sessionless /hydro-sse?dev=1 = %d, want 404 in a production build", rec.Code)
	}
}
