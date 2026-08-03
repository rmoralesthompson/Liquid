package liquid

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests are white-box for the same reason the CSRF codec's are: idle
// expiry needs a controllable clock, and the Limits defaulting contract lives
// on an unexported method. Everything else about them stays at the HTTP seam
// via ServeHTTP.

// tickClock is a hand-advanced clock.
type tickClock struct{ t time.Time }

func (c *tickClock) now() time.Time { return c.t }

// idleCounter is the minimal interactive component for clock-driven tests.
type idleCounter struct {
	HydroID string
	Count   int
}

func (c *idleCounter) Selector() string { return "app-idle-counter" }

func (c *idleCounter) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{ .Count }}</div>`
}

// Increment handles the counter's single allowlisted action.
func (c *idleCounter) Increment() { c.Count++ }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *idleCounter) Actions() []string { return []string{"Increment"} }

var (
	wbHydroIDPattern = regexp.MustCompile(`data-hydro-id="([A-Za-z0-9_-]+)"`)
	wbCSRFPattern    = regexp.MustCompile(`<meta name="liquid-csrf" content="([^"]+)">`)
)

// wbSession is what one render establishes: session cookie value, hydro
// token, CSRF token.
type wbSession struct {
	id    string
	hydro string
	csrf  string
}

// renderWB GETs / against the app directly and returns the established
// session.
func renderWB(t *testing.T, app *App) wbSession {
	t.Helper()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("render status = %d, want %d", rec.Code, http.StatusOK)
	}
	var id string
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sessionCookieName {
			id = ck.Value
		}
	}
	if id == "" {
		t.Fatal("render set no liquid_session cookie")
	}
	body := rec.Body.String()
	hm := wbHydroIDPattern.FindStringSubmatch(body)
	cm := wbCSRFPattern.FindStringSubmatch(body)
	if hm == nil || cm == nil {
		t.Fatalf("render missing hydro or csrf token: %q", body)
	}
	return wbSession{id: id, hydro: hm[1], csrf: cm[1]}
}

// fireWB POSTs a hydro event as the given session and returns the status.
func fireWB(t *testing.T, app *App, sess wbSession, action string) int {
	t.Helper()
	payload := fmt.Sprintf(`{"hydroId":%q,"action":%q,"csrfToken":%q}`, sess.hydro, action, sess.csrf)
	req := httptest.NewRequest(http.MethodPost, hydroEventPath, strings.NewReader(payload))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.id})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec.Code
}

func TestCSRFTokenExpiryTracksTheIdleWindow(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_700_000_000, 0)}
	app := New(WithLimits(Limits{SessionIdleTimeout: 2 * time.Hour}))
	app.now = clock.now
	if err := app.Route("/", &idleCounter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	sess := renderWB(t, app)

	// The token encodes expiryUnix:signature (D15 as amended by #45); its
	// expiry must track the configured idle window, not a fixed TTL.
	parts := strings.Split(sess.csrf, ":")
	if len(parts) != 2 {
		t.Fatalf("csrf token %q is not expiry:signature", sess.csrf)
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("csrf token expiry %q is not unix seconds: %v", parts[0], err)
	}
	if want := clock.t.Add(2 * time.Hour).Unix(); expiry != want {
		t.Errorf("token expiry = %d, want %d (render time + the idle window, D15/D2)", expiry, want)
	}
}

func TestCSRFTokenDoesNotDiscloseTheSessionID(t *testing.T) {
	app := New()
	if err := app.Route("/", &idleCounter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	sess := renderWB(t, app)

	// The session cookie is HttpOnly; the CSRF token is stamped into the
	// DOM (meta tag + hidden inputs). A token embedding the raw session ID
	// hands any DOM-reading script the cookie's value (#45, D15 as
	// amended): the token must be signature-only.
	if strings.Contains(sess.csrf, sess.id) {
		t.Errorf("csrf token %q embeds the session ID %q; the DOM must not carry the HttpOnly cookie's value", sess.csrf, sess.id)
	}
	if code := fireWB(t, app, sess, "Increment"); code != http.StatusOK {
		t.Errorf("event with the page's signature-only token = %d, want %d", code, http.StatusOK)
	}
}

func TestIdleSessionsExpireAndAreRemoved(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_700_000_000, 0)}
	app := New(WithLimits(Limits{SessionIdleTimeout: time.Minute}))
	app.now = clock.now
	if err := app.Route("/", &idleCounter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	idle := renderWB(t, app)
	clock.t = clock.t.Add(50 * time.Second)
	active := renderWB(t, app)
	// Now the first session has been idle past the one-minute window while
	// the second is well within it.
	clock.t = clock.t.Add(20 * time.Second)

	if status := fireWB(t, app, idle, "Increment"); status == http.StatusOK {
		t.Error("idle-expired session still dispatches events; want a refusal (D2/D20)")
	}
	if status := fireWB(t, app, active, "Increment"); status != http.StatusOK {
		t.Errorf("active session: status = %d, want %d", status, http.StatusOK)
	}

	// Expired means removed, not merely refused — idle garbage must not
	// occupy the bounded registry. The next registration sweeps it out.
	fresh := renderWB(t, app)
	if remaining := app.hydro.sessionCount(); remaining != 2 {
		t.Errorf("registry holds %d sessions after the sweep, want 2 (sessions %q and %q)", remaining, active.id, fresh.id)
	}
}

// sessionCount reports the number of live sessions, for asserting that
// expiry removes rather than merely refuses.
func (h *hydroRegistry) sessionCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

func TestLimitsWithDefaultsClampsZeroAndNegative(t *testing.T) {
	want := Limits{
		MaxSessions:             DefaultMaxSessions,
		MaxComponentsPerSession: DefaultMaxComponentsPerSession,
		SessionIdleTimeout:      DefaultSessionIdleTimeout,
		MaxEventBytes:           DefaultMaxEventBytes,
		MaxStreamsPerSession:    DefaultMaxStreamsPerSession,
	}
	// "No unlimited setting" is the documented contract (D20): a zero field
	// means its default, and a negative one must not slip through as
	// unbounded either.
	if got := (Limits{}).withDefaults(); got != want {
		t.Errorf("zero Limits withDefaults = %+v, want %+v", got, want)
	}
	negative := Limits{
		MaxSessions:             -1,
		MaxComponentsPerSession: -1,
		SessionIdleTimeout:      -1,
		MaxEventBytes:           -1,
		MaxStreamsPerSession:    -1,
	}
	if got := negative.withDefaults(); got != want {
		t.Errorf("negative Limits withDefaults = %+v, want %+v", got, want)
	}
}
