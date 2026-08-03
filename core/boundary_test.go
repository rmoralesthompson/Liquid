package liquid_test

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// These are the production browser↔app boundary pins the #29 threat-model
// audit found missing (#33): each control existed in code with no test
// holding it in place (THREAT-MODEL.md boundary 1).

func TestHydroEndpointsRejectWrongMethods(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/hydro-event"},
		{http.MethodHead, "/hydro-event"},
		{http.MethodPost, "/hydro-sse"},
		{http.MethodHead, "/hydro-sse"},
	} {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, http.StatusMethodNotAllowed)
		}
	}
}

func TestHeadOnPageRouteIsServed(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, err := http.Head(srv.URL + "/")
	if err != nil {
		t.Fatalf("HEAD /: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD / = %d, want %d — HEAD rides the GET path", resp.StatusCode, http.StatusOK)
	}
}

// TestRefusalOrderOnHydroEvent pins the refusal sequence as a sequence
// (cookie 404 → CSRF 403 → registry 404): each step is pinned in isolation
// elsewhere, but only a request failing several checks at once proves which
// check answers. CSRF answering before the registry is what keeps a
// cross-site request from probing which hydro tokens are live.
func TestRefusalOrderOnHydroEvent(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	// Forged CSRF and a bogus hydro token together: the CSRF 403 must win,
	// or an unproven request learns whether a registry key exists.
	forged := sess
	forged.csrf = "forged-token"
	forged.hydro = "bogus-hydro-id"
	if status, _ := fire(t, srv, forged, "Increment"); status != http.StatusForbidden {
		t.Errorf("forged CSRF + bogus hydro token = %d, want %d — CSRF must be checked before the registry", status, http.StatusForbidden)
	}

	// The same request without the session cookie: the cookie 404 must win
	// over the CSRF 403.
	anonymous := forged
	anonymous.id = ""
	if status, _ := fire(t, srv, anonymous, "Increment"); status != http.StatusNotFound {
		t.Errorf("no cookie + forged CSRF = %d, want %d — the cookie check comes first", status, http.StatusNotFound)
	}
}

// TestOversizedChunkedEventBodyIsRejectedMidRead drives the MaxBytesReader
// half of the body bound: a chunked request declares no Content-Length, so
// the declared-size guard cannot refuse it and the reader must stop it at
// the cap mid-decode (D20). The declared-size half is pinned by
// TestOversizedEventBodyIsRejectedBeforeDispatch.
func TestOversizedChunkedEventBodyIsRejectedMidRead(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	// io.MultiReader hides the size from http.NewRequest, so the request
	// goes out chunked with ContentLength unset.
	body := io.MultiReader(strings.NewReader(oversizedEvent(sess, liquid.DefaultMaxEventBytes)))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/hydro-event", body)
	if err != nil {
		t.Fatalf("building event request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sess.id})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /hydro-event: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked oversize = %d, want %d — MaxBytesReader must bound a body that declares no length (D20)", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

// initSpy counts OnInit calls through a DI-injected pointer, so a denied
// request can prove the lifecycle never started.
type initSpy struct{ calls atomic.Int32 }

// guardedProbe records lifecycle activity on its injected spy.
type guardedProbe struct {
	Spy *initSpy `inject:""`
}

func (p *guardedProbe) Selector() string { return "app-guarded-probe" }

func (p *guardedProbe) Template() string { return placeholderHTML }

// OnInit marks that the lifecycle ran.
func (p *guardedProbe) OnInit(liquid.Ctx) error {
	p.Spy.calls.Add(1)
	return nil
}

func TestDeniedRequestNeverRunsTheComponentLifecycle(t *testing.T) {
	spy := &initSpy{}
	app := liquid.New()
	if err := app.Provide(spy); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	deny := func(liquid.Ctx) liquid.GuardResult { return liquid.Deny() }
	if err := app.Route("/", &guardedProbe{}, liquid.WithGuard(deny)); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	resp, body := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied GET = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	if strings.Contains(body, placeholderHTML) {
		t.Errorf("denied response leaked the component render: %q", body)
	}
	if n := spy.calls.Load(); n != 0 {
		t.Errorf("OnInit ran %d times on a denied request, want 0 — guards run before instantiation (D4)", n)
	}
}

// cycleA and cycleB nest each other — the composition the runtime depth cap
// exists to cut, since build-time cycle detection is deferred.
type cycleA struct{}

func (c *cycleA) Selector() string { return "app-cycle-a" }

func (c *cycleA) Template() string { return `<div>{{liquidChild "app-cycle-b"}}</div>` }

type cycleB struct{}

func (c *cycleB) Selector() string { return "app-cycle-b" }

func (c *cycleB) Template() string { return `<div>{{liquidChild "app-cycle-a"}}</div>` }

func TestCyclicCompositionIsCutAtTheDepthCap(t *testing.T) {
	app := liquid.New()
	if err := app.Register(&cycleB{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &cycleA{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	// What the error page shows is flavor-dependent (the dev build's page
	// carries escaped detail by design, D18) — the pin here is only that the
	// cap turns the cycle into a 500 instead of unbounded recursion.
	resp, _ := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("cyclic composition = %d, want %d — the depth cap must fail the render, not recurse unbounded", resp.StatusCode, http.StatusInternalServerError)
	}
}

// unexportedParam tags a pathParam on an unexported field — bindable only
// by unsafe means, so registration must refuse it.
type unexportedParam struct {
	slug string `pathParam:"slug"` //nolint:unused // the tag is the point; binding must never reach the field
}

func (u *unexportedParam) Selector() string { return "app-unexported-param" }

func (u *unexportedParam) Template() string { return placeholderHTML }

func TestUnexportedPathParamFieldFailsRegistration(t *testing.T) {
	app := liquid.New()
	err := app.Route("/posts/:slug", &unexportedParam{})
	if err == nil {
		t.Fatal("Route accepted a pathParam tag on an unexported field; want a registration error")
	}
	if !strings.Contains(err.Error(), "exported") {
		t.Errorf("registration error %q does not say the field must be exported", err)
	}
}
