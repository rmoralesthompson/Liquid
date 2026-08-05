package liquid

import (
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests are white-box for the same reason the CSRF codec's and idle
// clock's are (hardening_test.go): the deterministic-render seams — the
// opaque-token source and the clock — are unexported, reachable only from
// package liquid. That is exactly the D28 invariant under test: a production
// consumer has no way in. Here we set all three non-deterministic inputs
// (token source, clock, CSRF secret) directly and prove a render is
// byte-stable; determinism_off_test.go proves the default build is not.

// fixedSource is a reproducible byte source (SHA-256 counter mode over a
// seed), the test-side twin of the liquiddev deterministicSource. It stands
// in for crypto/rand.Reader so two independently seeded Apps draw identical
// tokens.
type fixedSource struct {
	seed  uint64
	ctr   uint64
	block []byte
}

func (f *fixedSource) Read(p []byte) (int, error) {
	for i := range p {
		if len(f.block) == 0 {
			var in [16]byte
			binary.BigEndian.PutUint64(in[0:8], f.seed)
			binary.BigEndian.PutUint64(in[8:16], f.ctr)
			f.ctr++
			sum := sha256.Sum256(in[:])
			f.block = sum[:]
		}
		p[i] = f.block[0]
		f.block = f.block[1:]
	}
	return len(p), nil
}

// mapRangePage is interactive (so a random hydro token is minted) and ranges
// a map in its template (so map-iteration order is on the render path). Go's
// html/template sorts map keys, so the range is already stable — the
// byte-identical assertion pins that alongside the token and clock.
type mapRangePage struct {
	HydroID   string
	CSRFToken string
	Items     map[string]int
}

func (p *mapRangePage) Selector() string { return "app-map-range" }
func (p *mapRangePage) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><meta name="csrf" content="{{ .CSRFToken }}">` +
		`{{ range $k, $v := .Items }}<span>{{ $k }}={{ $v }}</span>{{ end }}</div>`
}
func (p *mapRangePage) Actions() []string { return nil }
func (p *mapRangePage) OnInit(Ctx) error {
	p.Items = map[string]int{"gamma": 3, "alpha": 1, "beta": 2}
	return nil
}

// newDeterministicApp builds an App with every framework non-determinism
// source pinned to a fixed value, the way WithDeterminism does under
// liquiddev but reachable from an untagged white-box test.
func newDeterministicApp(t *testing.T) *App {
	t.Helper()
	app := New()
	app.now = func() time.Time { return time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC) }
	app.rand = &fixedSource{seed: 0xD28}
	app.csrfSecret = []byte("deterministic-render-test-secret") // fixed 32 bytes
	if err := app.Route("/", &mapRangePage{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return app
}

// renderRoot serves GET / and returns the response body.
func renderRoot(t *testing.T, app *App) string {
	t.Helper()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200:\n%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestDeterministicRenderIsByteIdentical is the D28 acceptance test: with the
// token source, clock, and CSRF secret pinned, rendering the same component
// from two independently constructed Apps yields byte-identical HTML —
// tokens, CSRF token, and map-range order all stable.
func TestDeterministicRenderIsByteIdentical(t *testing.T) {
	first := renderRoot(t, newDeterministicApp(t))
	second := renderRoot(t, newDeterministicApp(t))
	if first != second {
		t.Fatalf("deterministic render is not byte-identical\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// Guard against a degenerate pass where nothing token-shaped rendered:
	// the hydro boundary and CSRF meta must actually be present.
	for _, want := range []string{"data-hydro-id=", `name="csrf"`, "alpha=1", "beta=2", "gamma=3"} {
		if !strings.Contains(first, want) {
			t.Errorf("render missing %q; the assertion would be vacuous:\n%s", want, first)
		}
	}
}

// TestDeterministicMapRangeIsSorted pins that framework-surfaced map
// iteration on the render path is stable: html/template renders map keys in
// sorted order, so the ranged output is alpha, beta, gamma regardless of
// insertion order.
func TestDeterministicMapRangeIsSorted(t *testing.T) {
	body := renderRoot(t, newDeterministicApp(t))
	ai, bi, gi := strings.Index(body, "alpha"), strings.Index(body, "beta"), strings.Index(body, "gamma")
	if !(ai >= 0 && ai < bi && bi < gi) {
		t.Errorf("map range not in sorted key order (alpha<beta<gamma): got positions %d,%d,%d\n%s", ai, bi, gi, body)
	}
}
