package liquid

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests are white-box because D14's registry rule — only components
// declaring their own [hydroId] get session entries — is about registry
// contents, which the HTTP seam cannot observe directly. The rendering
// itself still goes through ServeHTTP.

// wbCard is a plain child: no HydroID field, so no session entry.
type wbCard struct {
	Name string
}

func (c *wbCard) Selector() string { return "app-wb-card" }

func (c *wbCard) Template() string { return `<div class="card">{{ .Name }}</div>` }

// wbPanel is an interactive parent nesting wbCard.
type wbPanel struct {
	HydroID string
	Owner   string
}

func (c *wbPanel) Selector() string { return "app-wb-panel" }

func (c *wbPanel) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{liquidChild "app-wb-card" "name" .Owner}}</div>`
}

// entryCount returns the number of live component entries across the App's
// sessions.
func entryCount(a *App) int {
	a.hydro.mu.Lock()
	defer a.hydro.mu.Unlock()
	n := 0
	for _, elem := range a.hydro.sessions {
		n += elem.Value.(*hydroSession).lru.Len()
	}
	return n
}

// wbLive is an interactive child, nested by wbLivePanel below.
type wbLive struct {
	HydroID string
	Name    string
}

func (c *wbLive) Selector() string { return "app-wb-live" }

func (c *wbLive) Template() string {
	return `<div class="live" data-hydro-id="{{ .HydroID }}">{{ .Name }}</div>`
}

// wbLivePanel is an interactive parent nesting the interactive wbLive.
type wbLivePanel struct {
	HydroID string
	Owner   string
}

func (c *wbLivePanel) Selector() string { return "app-wb-live-panel" }

func (c *wbLivePanel) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{liquidChild "app-wb-live" "name" .Owner}}</div>`
}

func TestInteractiveNestedChildGetsSessionEntryBesideParent(t *testing.T) {
	app := New()
	if err := app.Register(&wbLive{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &wbLivePanel{Owner: "Ada"}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", rec.Code, rec.Body.String())
	}

	if got := entryCount(app); got != 2 {
		t.Errorf("registry entries after render = %d, want 2 — a nested child declaring [hydroId] gets its own entry (D14)", got)
	}
}

func TestPlainNestedChildGetsNoSessionEntry(t *testing.T) {
	app := New()
	if err := app.Register(&wbCard{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &wbPanel{Owner: "Ada"}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", rec.Code, rec.Body.String())
	}

	if got := entryCount(app); got != 1 {
		t.Errorf("registry entries after render = %d, want 1 — a plain nested child must ride its parent's entry, not get its own (D14)", got)
	}
}
