//go:build liquiddev

package liquidtest_test

import (
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// This end-to-end snapshot test runs only under the liquiddev build tag,
// because it turns on deterministic render mode (D28, core.WithDeterminism) —
// the opt-in that exists only there. Without pinned tokens/clock the golden
// files would diff on every run; with it, a full render and a fired patch each
// reproduce byte-for-byte. Regenerate the committed goldens with
// `go test -tags liquiddev -run TestSnapshot -update ./liquidtest`.

// snapCounter is a minimal interactive component: an interactive [hydroId]
// boundary plus one action that mutates visible state, so both a render
// snapshot and a post-event patch snapshot have something to pin.
type snapCounter struct {
	HydroID   string
	CSRFToken string
	Count     int
}

func (c *snapCounter) Selector() string { return "app-snap-counter" }
func (c *snapCounter) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="count">{{ .Count }}</span></div>`
}
func (c *snapCounter) Actions() []string { return []string{"Increment"} }
func (c *snapCounter) Increment()        { c.Count++ }

func newSnapHarness(t *testing.T) *liquidtest.Harness {
	t.Helper()
	app := liquid.New(liquid.WithDeterminism())
	if err := app.Route("/", &snapCounter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

// TestSnapshotRenderAndPatch is the D27 acceptance path: under deterministic
// mode the initial render matches a committed golden, and the patch from
// firing an action matches its own golden — both through MatchSnapshot,
// keyed on the same [hydroId] boundary.
func TestSnapshotRenderAndPatch(t *testing.T) {
	page := newSnapHarness(t).Get("/")
	page.MatchSnapshot("counter_initial")

	patch := page.Fire("Increment")
	patch.MatchSnapshot("counter_incremented")
}

// TestSnapshotIsStableAcrossRuns pins the zero-false-diff property: a second
// freshly constructed deterministic App renders the same component, and it
// still matches the golden the first run wrote — determinism makes the
// snapshot assertion meaningful rather than flaky.
func TestSnapshotIsStableAcrossRuns(t *testing.T) {
	newSnapHarness(t).Get("/").MatchSnapshot("counter_initial")
}
