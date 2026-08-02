package liquid_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// counter is the canonical interactive component: what liquid build emits for
// a .lsx with [hydroId] and a (click) binding, hand-written at the runtime
// seam.
type counter struct {
	HydroID string
	Count   int
}

func (c *counter) Selector() string { return "app-counter" }

func (c *counter) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="count">{{ .Count }}</span><button data-liquid-action="Increment">+1</button></div>`
}

// Increment handles the +1 button.
func (c *counter) Increment() { c.Count++ }

// Sneaky is exported and dispatchable-shaped but bound by no template event,
// so it must never be reachable through /hydro-event.
func (c *counter) Sneaky() { c.Count = 1_000_000 }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *counter) Actions() []string { return []string{"Increment"} }

var hydroIDPattern = regexp.MustCompile(`data-hydro-id="([A-Za-z0-9_-]+)"`)

var csrfPattern = regexp.MustCompile(`<meta name="liquid-csrf" content="([^"]+)">`)

// csrfToken extracts the liquid-csrf meta token from rendered HTML, failing
// the test when none is present.
func csrfToken(t *testing.T, body string) string {
	t.Helper()
	m := csrfPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no liquid-csrf meta token in body: %q", body)
	}
	return m[1]
}

// hydroID extracts the data-hydro-id token from rendered HTML, failing the
// test when none is present.
func hydroID(t *testing.T, body string) string {
	t.Helper()
	m := hydroIDPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no data-hydro-id token in body: %q", body)
	}
	return m[1]
}

// sessionCookie returns the liquid_session cookie from a response, or nil.
func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "liquid_session" {
			return c
		}
	}
	return nil
}

func TestInteractiveRenderSetsSessionCookieAndOpaqueHydroToken(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	resp, body := get(t, srv.URL+"/")

	ck := sessionCookie(resp)
	if ck == nil {
		t.Fatal("interactive render set no liquid_session cookie")
	}
	if !ck.HttpOnly {
		t.Error("liquid_session cookie must be HttpOnly")
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("liquid_session SameSite = %v, want Lax", ck.SameSite)
	}
	if !ck.Secure {
		t.Error("liquid_session cookie must be Secure")
	}
	if len(ck.Value) < 20 {
		t.Errorf("session ID %q is too short to be a credible random token", ck.Value)
	}

	token := hydroID(t, body)
	if len(token) < 20 {
		t.Errorf("hydro token %q is too short to be a credible random token", token)
	}
}

func TestEachRenderGetsAFreshHydroToken(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	_, first := get(t, srv.URL+"/")
	_, second := get(t, srv.URL+"/")

	if a, b := hydroID(t, first), hydroID(t, second); a == b {
		t.Errorf("two renders share hydro token %q; tokens must be fresh per render", a)
	}
}

func TestNonInteractiveRenderSetsNoSessionCookie(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, _ := get(t, srv.URL+"/")

	if ck := sessionCookie(resp); ck != nil {
		t.Errorf("plain component set a liquid_session cookie %q; only interactive renders need one", ck.Value)
	}
}

// envelope mirrors the D19 hydro response: an HTML patch or a redirect.
type envelope struct {
	Patch    string `json:"patch"`
	Redirect string `json:"redirect"`
}

// liveSession is what one interactive render establishes: the session
// cookie value, the hydro token, and the CSRF token.
type liveSession struct {
	id    string
	hydro string
	csrf  string
}

// renderInteractive GETs path and returns the session the render established.
func renderInteractive(t *testing.T, srv *httptest.Server, path string) liveSession {
	t.Helper()
	resp, body := get(t, srv.URL+path)
	ck := sessionCookie(resp)
	if ck == nil {
		t.Fatal("interactive render set no liquid_session cookie")
	}
	return liveSession{id: ck.Value, hydro: hydroID(t, body), csrf: csrfToken(t, body)}
}

// fire POSTs a hydro event as the given session, returning the response
// status and decoded envelope.
func fire(t *testing.T, srv *httptest.Server, sess liveSession, action string) (int, envelope) {
	t.Helper()
	sessionID := sess.id
	payload := fmt.Sprintf(`{"hydroId":%q,"action":%q,"csrfToken":%q}`, sess.hydro, action, sess.csrf)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/hydro-event", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("building event request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sess.id})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /hydro-event: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading event response: %v", err)
	}
	var env envelope
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("event response is not a JSON envelope: %v\n--- body ---\n%s", err, body)
		}
	}
	return resp.StatusCode, env
}

func TestClickDispatchMutatesLiveStateAndReturnsComponentPatch(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	status, env := fire(t, srv, sess, "Increment")

	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if want := `<span id="count">1</span>`; !strings.Contains(env.Patch, want) {
		t.Errorf("patch = %q, want it to contain %q", env.Patch, want)
	}
	if !strings.Contains(env.Patch, fmt.Sprintf("data-hydro-id=%q", sess.hydro)) {
		t.Errorf("patch = %q, want it rooted at the same hydro token", env.Patch)
	}
	if strings.Contains(env.Patch, "<!doctype") || strings.Contains(env.Patch, "<html") {
		t.Errorf("patch = %q, must carry the component render, not the document shell", env.Patch)
	}

	// A second event hits the same live instance: state accumulates.
	if _, env = fire(t, srv, sess, "Increment"); !strings.Contains(env.Patch, `<span id="count">2</span>`) {
		t.Errorf("second patch = %q, want the live instance to keep counting", env.Patch)
	}
}

func TestActionOutsideAllowlistIs404EvenWhenTheMethodExists(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	// Sneaky is a real exported method on counter, but no template binding
	// references it, so it is not in Actions() — dispatch must refuse it.
	status, _ := fire(t, srv, sess, "Sneaky")

	if status != http.StatusNotFound {
		t.Errorf("status = %d, want %d: dispatch must consult the compiled allowlist, never the method set", status, http.StatusNotFound)
	}
}

func TestUnknownHydroTokenOrMissingSessionIs404(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	badToken := sess
	badToken.hydro = "not-a-real-token"
	if status, _ := fire(t, srv, badToken, "Increment"); status != http.StatusNotFound {
		t.Errorf("unknown hydro token: status = %d, want %d", status, http.StatusNotFound)
	}
	noCookie := sess
	noCookie.id = ""
	if status, _ := fire(t, srv, noCookie, "Increment"); status != http.StatusNotFound {
		t.Errorf("missing session cookie: status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestMalformedEventBodyIs400(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/hydro-event", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("building event request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sess.id})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /hydro-event: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestFabricatedSessionCookieIsNotAdopted(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "liquid_session", Value: "attacker-chosen-id"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()

	ck := sessionCookie(resp)
	if ck == nil {
		t.Fatal("server adopted a session ID it never minted; want a fresh Set-Cookie")
	}
	if ck.Value == "attacker-chosen-id" {
		t.Fatal("server re-issued the attacker-chosen session ID")
	}
}

func TestPerSessionRegistryIsBoundedByEvictingOldestEntries(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	// Fill the session past its cap; the oldest entry must fall out while
	// the newest keeps working (64 is the per-session cap).
	var newest string
	for range 64 {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sess.id})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		newest = hydroID(t, string(body))
	}

	evicted := sess
	if status, _ := fire(t, srv, evicted, "Increment"); status != http.StatusNotFound {
		t.Errorf("evicted entry: status = %d, want %d", status, http.StatusNotFound)
	}
	newestSess := sess
	newestSess.hydro = newest
	if status, _ := fire(t, srv, newestSess, "Increment"); status != http.StatusOK {
		t.Errorf("newest entry: status = %d, want %d", status, http.StatusOK)
	}
}

func TestGlobalSessionRegistryIsBoundedByEvictingOldestSessions(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	first := renderInteractive(t, srv, "/")

	// Mint sessions past the global cap (1024); the first session must be
	// evicted wholesale.
	for range 1024 {
		resp, _ := get(t, srv.URL+"/")
		if sessionCookie(resp) == nil {
			t.Fatal("expected each cookieless render to mint a session")
		}
	}

	if status, _ := fire(t, srv, first, "Increment"); status != http.StatusNotFound {
		t.Errorf("evicted session: status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestRuntimeScriptIsServedAsAStaticFile(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	resp, body := get(t, srv.URL+"/liquid/runtime.js")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	for _, want := range []string{
		"data-liquid-action", "/hydro-event", "data-hydro-id",
		// The submit and CSRF halves of the loop (D12/D15): the script must
		// key on the compiled submit attribute, serialize the form, and send
		// the shell's token with every event.
		"data-liquid-submit", "csrfToken", `meta[name="liquid-csrf"]`, "FormData",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("runtime script missing %q", want)
		}
	}
}

func TestDocumentShellLoadsTheRuntimeScript(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	_, body := get(t, srv.URL+"/")

	if want := `<script src="/liquid/runtime.js" defer></script>`; !strings.Contains(body, want) {
		t.Errorf("page = %q, want it to load the runtime via %q", body, want)
	}
}

// misdeclared claims an action its struct does not implement as a func()
// method — the compiled-allowlist contract is broken, which must fail loudly
// at registration, not at dispatch.
type misdeclared struct {
	HydroID string
	Count   int
}

func (m *misdeclared) Selector() string { return "app-misdeclared" }

func (m *misdeclared) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{ .Count }}</div>`
}

func (m *misdeclared) Actions() []string { return []string{"Vanish"} }

func TestRouteRejectsAllowlistedActionWithoutMatchingHandler(t *testing.T) {
	app := liquid.New()

	if err := app.Route("/", &misdeclared{}); err == nil {
		t.Error("Route accepted an Actions() entry with no matching func() method; want a registration error")
	}
}
