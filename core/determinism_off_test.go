//go:build !liquiddev

package liquid

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

// This file is the D28 prod-unreachability pin, the twin of dev_off_test.go:
// it compiles only in a normal (non-liquiddev) build and proves that the
// deterministic-render mode is unreachable there, so the D15 CSPRNG invariant
// and the real clock provably hold in production. WithDeterminism does not
// exist in this build to be called; these assertions confirm the seams keep
// their production values with no way to override them.

var hydroIDAttr = regexp.MustCompile(`data-hydro-id="([^"]+)"`)

// TestProductionUsesTheRealCSPRNGAndClock pins that a default-constructed App
// draws tokens from crypto/rand.Reader and reads real wall time — the
// production values of the D28 seams, which no compiled path in this build
// can replace.
func TestProductionUsesTheRealCSPRNGAndClock(t *testing.T) {
	app := New()
	if app.rand != rand.Reader {
		t.Error("production App.rand is not crypto/rand.Reader — the CSPRNG token source (D15) was replaced")
	}
	before := time.Now()
	got := app.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Errorf("production App.now() = %v, want real wall time in [%v, %v]", got, before, after)
	}
}

// TestProductionRendersAreNotDeterministic pins the invariant behaviorally:
// two independently constructed Apps render the same interactive component
// with distinct hydro tokens. If a deterministic path had leaked into a
// production build, these would collide — which is exactly the failure D28
// forbids for prod.
func TestProductionRendersAreNotDeterministic(t *testing.T) {
	render := func() string {
		app := New()
		if err := app.Route("/", &mapRangePage{}); err != nil {
			t.Fatalf("Route: %v", err)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200", rec.Code)
		}
		m := hydroIDAttr.FindStringSubmatch(rec.Body.String())
		if m == nil {
			t.Fatalf("no data-hydro-id in render:\n%s", rec.Body.String())
		}
		return m[1]
	}
	if first, second := render(), render(); first == second {
		t.Errorf("two production renders share hydro token %q — the CSPRNG token path is not live", first)
	}
}
