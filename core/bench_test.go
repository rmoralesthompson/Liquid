package liquid_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// quietLogger discards runtime logs so benchmark output stays clean and slog
// formatting does not perturb the timing/allocation numbers. httptest's default
// example.com host would otherwise trip the plain-HTTP Secure-cookie warning
// on every request.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// These benchmarks establish an informational baseline at the two hot seams —
// static template render and live /hydro-event dispatch — so the project can
// answer "how does it perform?" with measured numbers rather than adjectives
// (D9). They are not a performance guarantee and make no comparative claim;
// see docs/benchmarks.md. Run with `make bench` (race off, which perturbs
// timing and allocation counts).
//
// Both seams are driven in-process through App.ServeHTTP against an
// httptest.Recorder — the real handler stack, no TCP socket — so the numbers
// reflect framework work, not loopback networking.

// The representative render tree: a three-level component nest (page → panel →
// card) with string interpolation at each level, wired through the compiled
// liquidChild seam. A child-bearing template is re-parsed per render, so this
// exercises a heavier path than a single leaf; BenchmarkRender measures both.

type benchCard struct{ Name string }

func (c *benchCard) Selector() string { return "bench-card" }
func (c *benchCard) Template() string { return `<div class="card">{{ .Name }}</div>` }

type benchPanel struct{ Title, Owner string }

func (c *benchPanel) Selector() string { return "bench-panel" }
func (c *benchPanel) Template() string {
	return `<section><h1>{{ .Title }}</h1>{{liquidChild "bench-card" "name" .Owner}}</section>`
}

type benchPage struct{ Dept, Lead string }

func (c *benchPage) Selector() string { return "bench-page" }
func (c *benchPage) Template() string {
	return `<main>{{liquidChild "bench-panel" "title" .Dept "owner" .Lead}}</main>`
}

// BenchmarkRender measures the static GET render path through the full handler
// stack, for a single leaf component and for the nested representative tree.
func BenchmarkRender(b *testing.B) {
	b.Run("Leaf", func(b *testing.B) {
		app := liquid.New(liquid.WithLogger(quietLogger()))
		if err := app.Route("/", &benchCard{Name: "Ada"}); err != nil {
			b.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("unexpected status %d", rec.Code)
			}
		}
	})

	b.Run("NestedTree", func(b *testing.B) {
		app := liquid.New(liquid.WithLogger(quietLogger()))
		if err := app.Register(&benchCard{}); err != nil {
			b.Fatal(err)
		}
		if err := app.Register(&benchPanel{}); err != nil {
			b.Fatal(err)
		}
		if err := app.Route("/", &benchPage{Dept: "Eng", Lead: "Ada"}); err != nil {
			b.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("unexpected status %d", rec.Code)
			}
		}
	})
}

// BenchmarkHydroEventDispatch measures a single /hydro-event round trip
// end-to-end through the real handler: CSRF check, session/allowlist lookup,
// action dispatch under the per-session mutex, subtree re-render, and envelope
// encode. One live interactive session is established once in setup (the GET
// render), then only the POST dispatch is timed. The CSRF token is a stateless
// HMAC bound to the session and its sliding expiry — not single-use — so the
// setup token stays valid across every iteration.
func BenchmarkHydroEventDispatch(b *testing.B) {
	app := liquid.New(liquid.WithLogger(quietLogger()))
	if err := app.Route("/", &counter{}); err != nil {
		b.Fatal(err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		b.Fatalf("setup render status %d", rec.Code)
	}
	body := rec.Body.String()
	ck := sessionCookie(rec.Result())
	if ck == nil {
		b.Fatal("no liquid_session cookie from interactive render")
	}
	hm := hydroIDPattern.FindStringSubmatch(body)
	cm := csrfPattern.FindStringSubmatch(body)
	if hm == nil || cm == nil {
		b.Fatalf("missing hydro id or csrf token in render: %q", body)
	}
	payload := fmt.Sprintf(`{"hydroId":%q,"action":"Increment","csrfToken":%q}`, hm[1], cm[1])

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/hydro-event", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "liquid_session", Value: ck.Value})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("unexpected dispatch status %d", rec.Code)
		}
	}
}
