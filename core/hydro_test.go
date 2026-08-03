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
	"time"

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

// eventPayload is the event body the runtime script would post for an
// action under this session.
func eventPayload(sess liveSession, action string) string {
	return fmt.Sprintf(`{"hydroId":%q,"action":%q,"csrfToken":%q}`, sess.hydro, action, sess.csrf)
}

// fire POSTs a hydro event as the given session, returning the response
// status and decoded envelope.
func fire(t *testing.T, srv *httptest.Server, sess liveSession, action string) (int, envelope) {
	t.Helper()
	sessionID := sess.id
	payload := eventPayload(sess, action)
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

func TestGlobalSessionCapIsConfigurable(t *testing.T) {
	app := liquid.New(liquid.WithLimits(liquid.Limits{MaxSessions: 2}))
	if err := app.Route("/", &counter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	first := renderInteractive(t, srv, "/")
	second := renderInteractive(t, srv, "/")
	third := renderInteractive(t, srv, "/")

	if status, _ := fire(t, srv, first, "Increment"); status != http.StatusNotFound {
		t.Errorf("session past the configured cap: status = %d, want %d", status, http.StatusNotFound)
	}
	for _, sess := range []liveSession{second, third} {
		if status, _ := fire(t, srv, sess, "Increment"); status != http.StatusOK {
			t.Errorf("session within the configured cap: status = %d, want %d", status, http.StatusOK)
		}
	}
}

// renderInSession GETs path as an existing session and returns the render's
// established tokens under that same session.
func renderInSession(t *testing.T, srv *httptest.Server, path, sessionID string) liveSession {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sessionID})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return liveSession{id: sessionID, hydro: hydroID(t, string(body)), csrf: csrfToken(t, string(body))}
}

// Pin (green on write): both caps flow through Limits since the configurable
// global cap landed; this fixes the per-session half in place.
func TestPerSessionComponentCapIsConfigurable(t *testing.T) {
	app := liquid.New(liquid.WithLimits(liquid.Limits{MaxComponentsPerSession: 2}))
	if err := app.Route("/", &counter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	first := renderInteractive(t, srv, "/")
	second := renderInSession(t, srv, "/", first.id)
	third := renderInSession(t, srv, "/", first.id)

	if status, _ := fire(t, srv, first, "Increment"); status != http.StatusNotFound {
		t.Errorf("entry past the configured cap: status = %d, want %d", status, http.StatusNotFound)
	}
	for _, sess := range []liveSession{second, third} {
		if status, _ := fire(t, srv, sess, "Increment"); status != http.StatusOK {
			t.Errorf("entry within the configured cap: status = %d, want %d", status, http.StatusOK)
		}
	}
}

func TestSessionEvictionIsLRUNotFIFO(t *testing.T) {
	app := liquid.New(liquid.WithLimits(liquid.Limits{MaxSessions: 2}))
	if err := app.Route("/", &counter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	oldest := renderInteractive(t, srv, "/")
	middle := renderInteractive(t, srv, "/")

	// Dispatching against the oldest session makes it the most recently
	// used; the next session past the cap must evict the untouched one.
	if status, _ := fire(t, srv, oldest, "Increment"); status != http.StatusOK {
		t.Fatalf("touching oldest session: status = %d, want %d", status, http.StatusOK)
	}
	newest := renderInteractive(t, srv, "/")

	if status, _ := fire(t, srv, middle, "Increment"); status != http.StatusNotFound {
		t.Errorf("least recently used session: status = %d, want %d (eviction must be LRU, D20)", status, http.StatusNotFound)
	}
	for name, sess := range map[string]liveSession{"touched": oldest, "newest": newest} {
		if status, _ := fire(t, srv, sess, "Increment"); status != http.StatusOK {
			t.Errorf("%s session: status = %d, want %d", name, status, http.StatusOK)
		}
	}
}

func TestComponentEvictionWithinASessionIsLRUNotFIFO(t *testing.T) {
	app := liquid.New(liquid.WithLimits(liquid.Limits{MaxComponentsPerSession: 2}))
	if err := app.Route("/", &counter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	oldest := renderInteractive(t, srv, "/")
	middle := renderInSession(t, srv, "/", oldest.id)

	// Dispatching against the oldest entry makes it the most recently used;
	// the next render past the cap must evict the untouched one.
	if status, _ := fire(t, srv, oldest, "Increment"); status != http.StatusOK {
		t.Fatalf("touching oldest entry: status = %d, want %d", status, http.StatusOK)
	}
	newest := renderInSession(t, srv, "/", oldest.id)

	if status, _ := fire(t, srv, middle, "Increment"); status != http.StatusNotFound {
		t.Errorf("least recently used entry: status = %d, want %d (eviction must be LRU, D20)", status, http.StatusNotFound)
	}
	for name, sess := range map[string]liveSession{"touched": oldest, "newest": newest} {
		if status, _ := fire(t, srv, sess, "Increment"); status != http.StatusOK {
			t.Errorf("%s entry: status = %d, want %d", name, status, http.StatusOK)
		}
	}
}

// fireRaw POSTs raw bytes to /hydro-event as the given session and returns
// the status code.
func fireRaw(t *testing.T, srv *httptest.Server, sessionID, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/hydro-event", strings.NewReader(body))
	if err != nil {
		t.Fatalf("building event request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sessionID})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /hydro-event: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// oversizedEvent builds an event that would dispatch fine were it not padded
// past limit bytes — so only the body bound can explain a rejection.
func oversizedEvent(sess liveSession, limit int) string {
	return fmt.Sprintf(`{"hydroId":%q,"action":"Increment","csrfToken":%q,"payload":{"pad":%q}}`,
		sess.hydro, sess.csrf, strings.Repeat("x", limit))
}

func TestOversizedEventBodyIsRejectedBeforeDispatch(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	status := fireRaw(t, srv, sess.id, oversizedEvent(sess, liquid.DefaultMaxEventBytes))

	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d: /hydro-event must bound the request body (D20)", status, http.StatusRequestEntityTooLarge)
	}
	// The live instance must be untouched by the refused event.
	if _, env := fire(t, srv, sess, "Increment"); !strings.Contains(env.Patch, `<span id="count">1</span>`) {
		t.Errorf("patch = %q, want count 1: the oversized event must not have dispatched", env.Patch)
	}
}

// blocker exposes one action that parks inside the handler and one that
// returns immediately, to observe whether two same-session dispatches can be
// inside handlers at once. The channels and scratch cell are package state
// because handlers hang off the component type; only one test uses them.
var (
	blockerEntered = make(chan struct{}, 1)
	blockerRelease = make(chan struct{})
	// blockerScratch is written unsynchronized by both handlers: if dispatch
	// ever lets them overlap, -race reports it even where timing hides it.
	blockerScratch int
)

type blocker struct{ HydroID string }

func (b *blocker) Selector() string { return "app-blocker" }

func (b *blocker) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">blocker</div>`
}

// Block parks until the test releases it, holding its dispatch slot.
func (b *blocker) Block() {
	blockerScratch++
	blockerEntered <- struct{}{}
	<-blockerRelease
}

// Poke returns immediately.
func (b *blocker) Poke() { blockerScratch++ }

// Actions mirrors the compiler-generated allowlist (D10).
func (b *blocker) Actions() []string { return []string{"Block", "Poke"} }

// fireAsync POSTs a hydro event from a goroutine — no t.Fatal off the test
// goroutine — closing done when the response lands.
func fireAsync(t *testing.T, srv *httptest.Server, sess liveSession, action string) (done chan struct{}) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/hydro-event", strings.NewReader(eventPayload(sess, action)))
	if err != nil {
		t.Fatalf("building event request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sess.id})
	done = make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
	return done
}

func TestSameSessionEventsSerializeAcrossComponentInstances(t *testing.T) {
	// Fresh channels per run: the release close below is one-shot, and a
	// reused package-level channel would poison -count=2 reruns.
	blockerEntered = make(chan struct{}, 1)
	blockerRelease = make(chan struct{})

	srv := newServer(t, "/", &blocker{})
	first := renderInteractive(t, srv, "/")
	second := renderInSession(t, srv, "/", first.id)

	blockDone := fireAsync(t, srv, first, "Block")
	<-blockerEntered

	// While the first instance's handler is parked, an event for a second
	// instance in the same session must wait its turn (D20.1).
	pokeDone := fireAsync(t, srv, second, "Poke")
	select {
	case <-pokeDone:
		t.Error("second component's handler ran while the first held the session; same-session dispatch must serialize (D20)")
	case <-time.After(100 * time.Millisecond):
	}

	close(blockerRelease)
	for _, done := range []chan struct{}{blockDone, pokeDone} {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("dispatch did not complete after release; possible deadlock")
		}
	}
}

// Pin (green on write): same-instance dispatch was already serialized; the
// session-level mutex keeps it that way. Fifty racing increments on one live
// instance must all land — interleaved handlers would lose updates, and
// -race would flag the unsynchronized Count writes.
func TestConcurrentEventsOnOneInstanceAllLand(t *testing.T) {
	srv := newServer(t, "/", &counter{})
	sess := renderInteractive(t, srv, "/")

	const events = 50
	var dones []chan struct{}
	for range events {
		dones = append(dones, fireAsync(t, srv, sess, "Increment"))
	}
	for _, done := range dones {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent dispatch did not complete")
		}
	}

	if _, env := fire(t, srv, sess, "Increment"); !strings.Contains(env.Patch, fmt.Sprintf(`<span id="count">%d</span>`, events+1)) {
		t.Errorf("patch = %q, want count %d: every serialized event must land", env.Patch, events+1)
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
		// The push half of the loop (D3/D20): the script must open the
		// session's SSE stream, apply pushed patches by hydro id, and turn a
		// reconnect into a full re-render of current state.
		"EventSource", "/hydro-sse", "location.reload", "EventSource.CLOSED",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("runtime script missing %q", want)
		}
	}
}

// TestRuntimeScriptGuardsRedirectScheme pins the redirect sink (#30): a
// redirect answer is author-supplied and rides the envelope verbatim, so the
// runtime must resolve the target and navigate only to http(s) — a
// javascript:/data: scheme handed straight to location.assign would execute
// in the page origin, turning the documented open-redirect (author
// responsibility) into DOM XSS. Pinned by string presence per AR-2 (runtime.js
// has no repeatable JS-level test); the negative assertion guards against a
// regression back to the raw sink.
func TestRuntimeScriptGuardsRedirectScheme(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	_, body := get(t, srv.URL+"/liquid/runtime.js")

	for _, want := range []string{
		// The redirect goes through navigate(), which resolves the target and
		// gates on protocol before assigning.
		"function navigate(", "new URL(", "dest.protocol", `"http:"`, `"https:"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("runtime script missing redirect-scheme guard %q", want)
		}
	}
	// The raw sink must never reappear: assigning the envelope value straight
	// to location without the scheme gate is the exact regression this pins.
	if strings.Contains(body, "assign(env.redirect)") {
		t.Error("runtime script assigns env.redirect to location without the scheme guard")
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
