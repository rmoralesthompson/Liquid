package liquid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authComp exercises the event-path auth surface: read identity, log in, log out.
type authComp struct {
	HydroID string
	Who     string
}

func (c *authComp) Selector() string { return "app-auth" }
func (c *authComp) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="who">{{ .Who }}</span></div>`
}

func (c *authComp) SignIn(e Event) {
	if err := e.Login("user-42"); err != nil {
		c.Who = "error"
		return
	}
	c.Who = "in"
}
func (c *authComp) SignOut(e Event) { _ = e.Logout(); c.Who = "out" }
func (c *authComp) Whoami(e Event) {
	if p, ok := e.Principal(); ok {
		c.Who = p
	} else {
		c.Who = "anon"
	}
}
func (c *authComp) Actions() []string { return []string{"SignIn", "SignOut", "Whoami"} }

// authClient mimics a browser: it carries cookies across events, applies
// Set-Cookie (rotation, identity, logout-clear), and tracks the current CSRF.
type authClient struct {
	app     *App
	cookies map[string]string
	hydroID string
	csrf    string
}

func newAuthClient(t *testing.T, app *App) *authClient {
	t.Helper()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	c := &authClient{app: app, cookies: map[string]string{}}
	for _, ck := range rec.Result().Cookies() {
		c.cookies[ck.Name] = ck.Value
	}
	body := rec.Body.String()
	c.hydroID = submatch(t, metricHydroRe, body)
	c.csrf = submatch(t, metricCSRFRe, body)
	return c
}

func (c *authClient) fire(t *testing.T, action string) (int, Envelope) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"hydroId": c.hydroID, "action": action, "csrfToken": c.csrf})
	req := httptest.NewRequest(http.MethodPost, hydroEventPath, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for name, val := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: val})
	}
	rec := httptest.NewRecorder()
	c.app.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
	}
	var env Envelope
	if rec.Code == http.StatusOK {
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.CSRF != "" {
			c.csrf = env.CSRF
		}
	}
	return rec.Code, env
}

// TestLoginRotatesSessionAndAttachesIdentity is the end-to-end auth flow: an
// anonymous session logs in (rotating the session id and gaining an identity),
// reads back its principal, then logs out (rotating again, dropping identity).
func TestLoginRotatesSessionAndAttachesIdentity(t *testing.T) {
	app := New()
	if err := app.Route("/", &authComp{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	c := newAuthClient(t, app)
	anonSession := c.cookies[sessionCookieName]

	if _, env := c.fire(t, "Whoami"); !strings.Contains(env.Patch, ">anon<") {
		t.Fatalf("before login, Whoami = %q, want anon", env.Patch)
	}

	if code, env := c.fire(t, "SignIn"); code != http.StatusOK || strings.Contains(env.Patch, "error") {
		t.Fatalf("SignIn failed: code=%d patch=%q", code, env.Patch)
	}
	if c.cookies[sessionCookieName] == anonSession {
		t.Error("session id was not rotated on login — session-fixation defense missing (D15)")
	}
	if c.cookies[authCookieName] == "" {
		t.Error("no identity cookie set on login")
	}

	if _, env := c.fire(t, "Whoami"); !strings.Contains(env.Patch, ">user-42<") {
		t.Errorf("after login, Whoami = %q, want user-42", env.Patch)
	}

	loggedInSession := c.cookies[sessionCookieName]
	c.fire(t, "SignOut")
	if c.cookies[sessionCookieName] == loggedInSession {
		t.Error("session id was not rotated on logout")
	}
	if _, ok := c.cookies[authCookieName]; ok {
		t.Error("identity cookie was not cleared on logout")
	}
	if _, env := c.fire(t, "Whoami"); !strings.Contains(env.Patch, ">anon<") {
		t.Errorf("after logout, Whoami = %q, want anon", env.Patch)
	}
}

// TestLoginInvalidatesOldSessionCSRF proves the fixation defense end to end: a
// pre-login CSRF token, replayed after the login rotated the session, is refused.
func TestLoginInvalidatesOldSessionCSRF(t *testing.T) {
	app := New()
	if err := app.Route("/", &authComp{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	c := newAuthClient(t, app)
	staleSession := c.cookies[sessionCookieName]
	staleCSRF := c.csrf

	c.fire(t, "SignIn") // rotates session; c now tracks the new session+csrf

	// Replay the pre-login session cookie + CSRF: must be refused (403/404),
	// never dispatched.
	body, _ := json.Marshal(map[string]string{"hydroId": c.hydroID, "action": "Whoami", "csrfToken": staleCSRF})
	req := httptest.NewRequest(http.MethodPost, hydroEventPath, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: staleSession})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("a pre-login session+CSRF was still accepted after rotation (status 200) — fixation not defended")
	}
}

// TestRequireAuthenticatedGuard denies anonymous requests and admits ones
// carrying a valid identity cookie.
func TestRequireAuthenticatedGuard(t *testing.T) {
	app := New()
	if err := app.Route("/private", &authComp{}, WithGuard(RequireAuthenticated())); err != nil {
		t.Fatalf("Route: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/private", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("anonymous access = %d, want 403", rec.Code)
	}

	sid := "session-xyz"
	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: mintAuthCookie(app.authSecret, sid, "user-9", DefaultAuthTTL, app.now())})
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("authenticated access = %d, want 200", rec.Code)
	}
}

// loginAtInit tries to log in during render — which must be refused (ADR-0007:
// Login is an event-path operation).
type loginAtInit struct {
	HydroID string
	State   string
}

func (c *loginAtInit) Selector() string { return "app-login-init" }
func (c *loginAtInit) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="s">{{ .State }}</span></div>`
}
func (c *loginAtInit) OnInit(ctx Ctx) error {
	if err := ctx.Login("x"); err != nil {
		c.State = "refused"
	} else {
		c.State = "allowed"
	}
	return nil
}

func TestLoginFromRenderPathIsRefused(t *testing.T) {
	app := New()
	if err := app.Route("/", &loginAtInit{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if body := rec.Body.String(); !strings.Contains(body, ">refused<") {
		t.Errorf("Login during OnInit was not refused; body = %q", body)
	}
}
