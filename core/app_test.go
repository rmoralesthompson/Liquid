package liquid_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// helloWorldHTML is hello's rendered output for Name "world".
const helloWorldHTML = "<h1>Hello, world!</h1>"

// neverRenderedHTML marks templates that a failing lifecycle must not reach.
const neverRenderedHTML = "<p>never rendered</p>"

type hello struct {
	Name string
}

func (h *hello) Selector() string { return "app-hello" }

func (h *hello) Template() string { return "<h1>Hello, {{ .Name }}!</h1>" }

func newServer(t *testing.T, path string, c liquid.Component) *httptest.Server {
	t.Helper()
	app := liquid.New()
	if err := app.Route(path, c); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return resp, string(body)
}

func TestRouteRendersComponentAsHTML(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, body := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if want := helloWorldHTML; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

func TestInterpolatedFieldsAreContextuallyEscaped(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "<script>alert(1)</script>"})

	_, body := get(t, srv.URL+"/")

	if strings.Contains(body, "<script>") {
		t.Fatalf("body contains unescaped markup: %q", body)
	}
	if want := "&lt;script&gt;"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain escaped %q", body, want)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, _ := get(t, srv.URL+"/nope")

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

type brokenTemplate struct{}

func (b *brokenTemplate) Selector() string { return "app-broken" }

func (b *brokenTemplate) Template() string { return "{{ .Oops" }

func TestInvalidTemplateFailsAtRegistrationNotRequestTime(t *testing.T) {
	app := liquid.New()

	if err := app.Route("/", &brokenTemplate{}); err == nil {
		t.Fatal("Route accepted an invalid template; want an error at registration")
	}
}

type withItems struct {
	Items []string
}

func (w *withItems) Selector() string { return "app-items" }

func (w *withItems) Template() string { return "<p>items</p>" }

func TestRouteRejectsPrototypesWithSharedReferenceFields(t *testing.T) {
	app := liquid.New()

	if err := app.Route("/", &withItems{Items: []string{"a"}}); err == nil {
		t.Error("Route accepted a prototype with a non-nil slice field; a shallow per-request copy would share it across requests")
	}
	if err := app.Route("/", &withItems{}); err != nil {
		t.Errorf("Route rejected a prototype whose reference fields are all nil: %v", err)
	}
}

func TestNonGETMethodsAreRejected(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

type boom struct{}

func (b *boom) Selector() string { return "app-boom" }

func (b *boom) Template() string { return "{{ .Detonate }}" }

func TestRenderFailureIs500AndLoggedToInjectedLogger(t *testing.T) {
	var logs strings.Builder
	app := liquid.New(liquid.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := app.Route("/", &boom{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, _ := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if !strings.Contains(logs.String(), "rendering component") {
		t.Errorf("injected logger saw no render error; logs: %q", logs.String())
	}
}

type userCard struct {
	UserID string `pathParam:"userId"`
}

func (u *userCard) Selector() string { return "app-user-card" }

func (u *userCard) Template() string { return "<p>user {{ .UserID }}</p>" }

func TestPathParamSegmentBindsToTaggedField(t *testing.T) {
	srv := newServer(t, "/users/:userId", &userCard{})

	resp, body := get(t, srv.URL+"/users/42")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %q)", resp.StatusCode, http.StatusOK, body)
	}
	if want := "<p>user 42</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

// newGuardedServer registers c at path with the given guard.
func newGuardedServer(t *testing.T, path string, c liquid.Component, g liquid.Guard) *httptest.Server {
	t.Helper()
	app := liquid.New()
	if err := app.Route(path, c, liquid.WithGuard(g)); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func TestDenyingGuardBlocksRenderWith403(t *testing.T) {
	srv := newGuardedServer(t, "/admin", &hello{Name: "secret"},
		func(ctx liquid.Ctx) liquid.GuardResult { return liquid.Deny() })

	resp, body := get(t, srv.URL+"/admin")

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	if strings.Contains(body, "secret") {
		t.Errorf("denied response leaked component output: %q", body)
	}
}

func TestAllowingGuardLetsTheRenderThrough(t *testing.T) {
	srv := newGuardedServer(t, "/admin", &hello{Name: "world"},
		func(ctx liquid.Ctx) liquid.GuardResult { return liquid.Allow() })

	resp, body := get(t, srv.URL+"/admin")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if want := helloWorldHTML; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

func TestRedirectingGuardRespondsWithRedirect(t *testing.T) {
	srv := newGuardedServer(t, "/account", &hello{Name: "secret"},
		func(ctx liquid.Ctx) liquid.GuardResult { return liquid.Redirect("/login") })

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + "/account")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want %q", loc, "/login")
	}
}

type profile struct {
	Who   string
	Theme string
}

func (p *profile) Selector() string { return "app-profile" }

func (p *profile) Template() string { return "<p>{{ .Who }}/{{ .Theme }}</p>" }

func (p *profile) OnInit(ctx liquid.Ctx) error {
	p.Who = ctx.Param("who")
	p.Theme = ctx.Query("theme")
	return nil
}

func TestOnInitRunsBeforeRenderWithRequestAccessors(t *testing.T) {
	srv := newServer(t, "/p/:who", &profile{})

	resp, body := get(t, srv.URL+"/p/ada?theme=dark")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %q)", resp.StatusCode, http.StatusOK, body)
	}
	if want := "<p>ada/dark</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

type failingInit struct{}

func (f *failingInit) Selector() string { return "app-failing" }

func (f *failingInit) Template() string { return neverRenderedHTML }

func (f *failingInit) OnInit(ctx liquid.Ctx) error {
	return fmt.Errorf("db down")
}

func TestOnInitErrorRendersFrameworkErrorPageWithoutLeaking(t *testing.T) {
	var logs strings.Builder
	app := liquid.New(liquid.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := app.Route("/", &failingInit{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, body := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want the framework error page as text/html", ct)
	}
	if !strings.Contains(body, "Something went wrong") {
		t.Errorf("body = %q, want the framework error page", body)
	}
	if strings.Contains(body, "db down") || strings.Contains(body, "never rendered") {
		t.Errorf("error page leaked internals: %q", body)
	}
	if !strings.Contains(logs.String(), "db down") {
		t.Errorf("error detail must reach the log; logs: %q", logs.String())
	}
}

type panicky struct{}

func (p *panicky) Selector() string { return "app-panicky" }

func (p *panicky) Template() string { return neverRenderedHTML }

func (p *panicky) OnInit(ctx liquid.Ctx) error { panic("boom") }

func TestPanicInOnInitIsRecoveredTo500AndProcessStaysAlive(t *testing.T) {
	var logs strings.Builder
	app := liquid.New(liquid.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := app.Route("/boom", &panicky{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if err := app.Route("/", &hello{Name: "world"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, _ := get(t, srv.URL+"/boom")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if !strings.Contains(logs.String(), "boom") {
		t.Errorf("panic value must reach the log; logs: %q", logs.String())
	}

	resp, body := get(t, srv.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after a recovered panic, status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if want := helloWorldHTML; !strings.Contains(body, want) {
		t.Errorf("after a recovered panic, body = %q, want it to contain %q", body, want)
	}
}

type probeKey struct{}

type ctxProbe struct {
	FromRequest string
}

func (c *ctxProbe) Selector() string { return "app-ctx-probe" }

func (c *ctxProbe) Template() string { return "<p>{{ .FromRequest }}</p>" }

func (c *ctxProbe) OnInit(ctx liquid.Ctx) error {
	if v, ok := ctx.Value(probeKey{}).(string); ok {
		c.FromRequest = v
	}
	return nil
}

func TestCtxEmbedsTheRequestContext(t *testing.T) {
	var _ context.Context = liquid.Ctx{} // the D18 contract, checked at compile time

	app := liquid.New()
	if err := app.Route("/", &ctxProbe{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), probeKey{}, "from-request")))
	}))
	t.Cleanup(srv.Close)

	_, body := get(t, srv.URL+"/")

	if want := "<p>from-request</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q — OnInit's ctx must be the request's context", body, want)
	}
}

func TestLiteralSegmentBeatsParamRegardlessOfRegistrationOrder(t *testing.T) {
	app := liquid.New()
	if err := app.Route("/users/:userId", &userCard{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if err := app.Route("/users/new", &hello{Name: "signup"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	_, body := get(t, srv.URL+"/users/new")
	if want := "<h1>Hello, signup!</h1>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want the literal route %q — a later literal must not be shadowed by an earlier :param", body, want)
	}

	_, body = get(t, srv.URL+"/users/42")
	if want := "<p>user 42</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want the param route %q", body, want)
	}
}

func TestEscapedPathSegmentBindsItsDecodedValue(t *testing.T) {
	srv := newServer(t, "/users/:userId", &userCard{})

	resp, body := get(t, srv.URL+"/users/a%2Fb")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d — %%2F is one segment, not a separator (body: %q)", resp.StatusCode, http.StatusOK, body)
	}
	if want := "<p>user a/b</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want the decoded param in %q", body, want)
	}
}

func TestEmptyPathSegmentsDoNotMatch(t *testing.T) {
	srv := newServer(t, "/users/:userId", &userCard{})

	for _, path := range []string{"/users//42", "//users/42"} {
		resp, _ := get(t, srv.URL+path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d — empty segments never match", path, resp.StatusCode, http.StatusNotFound)
		}
	}

	// A single trailing slash is tolerated as URL sloppiness.
	resp, _ := get(t, srv.URL+"/users/42/")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /users/42/: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

type aborting struct{}

func (a *aborting) Selector() string { return "app-aborting" }

func (a *aborting) Template() string { return neverRenderedHTML }

func (a *aborting) OnInit(ctx liquid.Ctx) error { panic(http.ErrAbortHandler) }

func TestErrAbortHandlerPanicIsNotConvertedToAnErrorPage(t *testing.T) {
	app := liquid.New(liquid.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err := app.Route("/", &aborting{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		t.Fatalf("status = %d; want the net/http abort sentinel to sever the connection, not produce a response", resp.StatusCode)
	}
}

type intParam struct {
	ID int `pathParam:"id"`
}

func (i *intParam) Selector() string { return "app-int-param" }

func (i *intParam) Template() string { return "<p>x</p>" }

func TestRouteRejectsPathParamTagOnNonStringField(t *testing.T) {
	app := liquid.New()

	if err := app.Route("/x/:id", &intParam{}); err == nil {
		t.Error("Route accepted a pathParam tag on an int field; v0.1 binds strings only and must say so at registration")
	}
}

func TestConcurrentRequestsEachRenderAFreshInstance(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	const n = 25
	errs := make(chan error, n)
	for range n {
		go func() {
			resp, err := http.Get(srv.URL + "/")
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errs <- err
				return
			}
			if !strings.Contains(string(body), helloWorldHTML) {
				errs <- fmt.Errorf("unexpected body: %q", body)
				return
			}
			errs <- nil
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
