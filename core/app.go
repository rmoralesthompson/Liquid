package liquid

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

// App routes HTTP requests to components. Component templates are parsed
// once, at registration; every request renders a fresh component instance —
// registered instances act only as prototypes and are never shared mutable
// state across requests.
type App struct {
	logger     *slog.Logger
	routes     []*route
	services   []reflect.Value // Provide'd singletons, in registration order
	static     http.Handler    // file server mounted at /static/, nil until Static
	hydro      hydroRegistry   // live interactive instances (D15)
	csrfSecret []byte          // HMAC key for CSRF tokens, minted per process (D15)
}

type route struct {
	pattern      []string      // path segments; a ":name" segment binds a param
	prototype    reflect.Value // pointer to the registered component struct
	tmpl         *template.Template
	guards       []Guard
	injections   []injection       // dependencies resolved at registration (D8)
	fallbackHead Head              // shell head for components without HeadProvider
	hydroField   int               // index of the HydroID field; -1 when not interactive
	csrfField    int               // index of the CSRFToken field; -1 when the component has none
	actions      map[string]action // allowlisted action → dispatch shape, resolved at registration (D10)
}

// RouteOption configures one route at registration.
type RouteOption func(*route)

// WithGuard adds a CanActivate guard to the route. Guards run in the order
// they were added; the first non-allow verdict decides the response.
func WithGuard(g Guard) RouteOption {
	return func(rt *route) { rt.guards = append(rt.guards, g) }
}

// splitPath breaks a registration path into its segments; "/" has none.
func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// splitRequestPath breaks an escaped request path into decoded segments. A
// single trailing slash is tolerated as URL sloppiness; empty segments and
// undecodable escapes never match any route (ok is false).
func splitRequestPath(escaped string) ([]string, bool) {
	trimmed := strings.TrimPrefix(escaped, "/")
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return nil, true
	}
	segs := strings.Split(trimmed, "/")
	for i, seg := range segs {
		if seg == "" {
			return nil, false
		}
		dec, err := url.PathUnescape(seg)
		if err != nil {
			return nil, false
		}
		segs[i] = dec
	}
	return segs, true
}

// moreSpecific reports whether pattern p beats pattern q for the same
// matched path: at the first position where their segment kinds differ, a
// literal segment wins over a :param.
func moreSpecific(p, q []string) bool {
	for i := range p {
		pLit := !strings.HasPrefix(p[i], ":")
		qLit := !strings.HasPrefix(q[i], ":")
		if pLit != qLit {
			return pLit
		}
	}
	return false
}

// match reports whether segs satisfies the route's pattern, returning the
// values bound by :name segments.
func (rt *route) match(segs []string) (map[string]string, bool) {
	if len(segs) != len(rt.pattern) {
		return nil, false
	}
	var params map[string]string
	for i, p := range rt.pattern {
		if name, ok := strings.CutPrefix(p, ":"); ok {
			if params == nil {
				params = make(map[string]string)
			}
			params[name] = segs[i]
			continue
		}
		if p != segs[i] {
			return nil, false
		}
	}
	return params, true
}

// Option configures an App at construction.
type Option func(*App)

// WithLogger sets the slog logger the App uses for runtime errors. Without
// it, the App logs through slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(a *App) { a.logger = l }
}

// New creates an App, applying any options.
func New(opts ...Option) *App {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// No entropy at construction is unrecoverable and must not fall
		// through to serving unsigned tokens.
		panic(fmt.Sprintf("liquid: generating CSRF secret: %v", err))
	}
	a := &App{
		logger:     slog.Default(),
		csrfSecret: secret,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Route registers a component to serve GET requests at path. A ":name"
// segment matches any single path segment and binds its decoded value to the
// component field tagged `pathParam:"name"`. When several routes match a
// request, a literal segment beats a :param at the first position they
// differ; exact ties fall to registration order. The component's
// template is parsed immediately; a template error is reported here, at
// registration, never at request time. The instance passed in is a prototype:
// its field values seed each per-request copy, so its reference-typed fields
// (slices, maps, pointers, …) must be nil — a shallow copy of a live
// reference would be shared mutable state across requests. Per-request data
// belongs in lifecycle hooks, not the prototype.
func (a *App) Route(path string, c Component, opts ...RouteOption) error {
	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("registering route %s: component must be a pointer to a struct, got %T", path, c)
	}
	if err := validatePrototype(v.Elem(), v.Elem().Type().Name()); err != nil {
		return fmt.Errorf("registering route %s: %w", path, err)
	}
	if err := validatePathParamTags(v.Elem().Type()); err != nil {
		return fmt.Errorf("registering route %s: %w", path, err)
	}

	injections, err := a.resolveInjections(v.Elem().Type())
	if err != nil {
		return fmt.Errorf("registering route %s: %w", path, err)
	}
	actions, err := resolveActions(v)
	if err != nil {
		return fmt.Errorf("registering route %s: %w", path, err)
	}

	tmpl, err := template.New(c.Selector()).Parse(c.Template())
	if err != nil {
		return fmt.Errorf("parsing template for %s: %w", c.Selector(), err)
	}

	rt := &route{
		pattern:      splitPath(path),
		prototype:    v,
		tmpl:         tmpl,
		injections:   injections,
		fallbackHead: Head{Title: c.Selector()},
		hydroField:   hydroIDField(v.Elem().Type()),
		csrfField:    csrfTokenField(v.Elem().Type()),
		actions:      actions,
	}
	for _, opt := range opts {
		opt(rt)
	}
	a.routes = append(a.routes, rt)
	return nil
}

// validatePathParamTags rejects pathParam tags the router cannot bind:
// v0.1 binds string fields only, and the field must be exported. Failing at
// registration keeps misconfiguration loud, matching the template-error
// posture. A tag naming a param absent from this route's path is fine — the
// same component may serve several routes.
func validatePathParamTags(t reflect.Type) error {
	for i := range t.NumField() {
		f := t.Field(i)
		if _, ok := f.Tag.Lookup("pathParam"); !ok {
			continue
		}
		if !f.IsExported() {
			return fmt.Errorf("field %s.%s: pathParam binding requires an exported field", t.Name(), f.Name)
		}
		if f.Type.Kind() != reflect.String {
			return fmt.Errorf("field %s.%s: pathParam binding supports string fields in v0.1, got %s", t.Name(), f.Name, f.Type.Kind())
		}
	}
	return nil
}

// bindPathParams copies matched :param values into the instance's fields by
// their pathParam struct tags (v0.1: string fields).
func bindPathParams(structV reflect.Value, params map[string]string) {
	t := structV.Type()
	for i := range structV.NumField() {
		name, ok := t.Field(i).Tag.Lookup("pathParam")
		if !ok {
			continue
		}
		val, ok := params[name]
		if !ok {
			continue
		}
		f := structV.Field(i)
		if f.Kind() == reflect.String && f.CanSet() {
			f.SetString(val)
		}
	}
}

// validatePrototype rejects prototype structs holding non-nil reference
// values, walking nested structs. Nil reference fields are fine: they copy
// as nil and each request may populate its own.
func validatePrototype(structV reflect.Value, path string) error {
	t := structV.Type()
	for i := range structV.NumField() {
		f := structV.Field(i)
		name := path + "." + t.Field(i).Name
		switch f.Kind() {
		case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
			if !f.IsNil() {
				return fmt.Errorf("prototype field %s holds a non-nil %s, which a per-request copy would share across requests; leave it nil", name, f.Kind())
			}
		case reflect.Struct:
			if err := validatePrototype(f, name); err != nil {
				return err
			}
		default:
		}
	}
	return nil
}

// errorPageHTML is the framework error page: clean by design — the
// underlying error goes to the log, never to the client (D18; dev-mode
// diagnostics are a later slice).
const errorPageHTML = `<!doctype html>
<html><head><title>500 · Liquid</title></head>
<body><h1>Something went wrong</h1><p>The server hit an error handling this request.</p></body></html>
`

// errorPage writes the framework error page as a 500 response.
func (a *App) errorPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte(errorPageHTML)); err != nil {
		a.logger.Error("writing error page", "error", err)
	}
}

// ServeHTTP implements http.Handler. A panic anywhere in the request path
// (guards, OnInit, render) is recovered to the framework error page — the
// render is buffered, so no body bytes precede it (D18).
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
			panic(rec) // net/http's own abort sentinel — not ours to handle
		}
		a.logger.Error("panic serving request", "path", r.URL.Path, "panic", rec)
		a.errorPage(w)
	}()

	if r.URL.Path == hydroEventPath {
		a.serveHydroEvent(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == runtimeScriptPath {
		a.serveRuntimeScript(w)
		return
	}
	if a.static != nil && strings.HasPrefix(r.URL.Path, staticPrefix) {
		a.static.ServeHTTP(cacheOnSuccess{w}, r)
		return
	}
	segs, ok := splitRequestPath(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	rt, params := a.matchRoute(segs)
	if rt == nil {
		http.NotFound(w, r)
		return
	}

	ctx := Ctx{Context: r.Context(), params: params, req: r}
	if !a.runGuards(w, r, rt, ctx) {
		return
	}
	a.renderRoute(w, r, rt, params, ctx)
}

// mintRenderTokens establishes one session-bound render's values: the
// session ID, a fresh hydro token when the component is interactive, and
// the render's CSRF token (D15).
func (a *App) mintRenderTokens(w http.ResponseWriter, r *http.Request, rt *route) (sessionID, hydroID, csrf string, err error) {
	if sessionID, err = a.ensureSession(w, r); err == nil && rt.hydroField >= 0 {
		hydroID, err = randomToken()
	}
	if err != nil {
		return "", "", "", err
	}
	return sessionID, hydroID, mintCSRF(a.csrfSecret, sessionID, time.Now()), nil
}

// matchRoute returns the most specific registered route matching the path
// segments, with its bound params, or nil when nothing matches.
func (a *App) matchRoute(segs []string) (*route, map[string]string) {
	var best *route
	var params map[string]string
	for _, cand := range a.routes {
		p, ok := cand.match(segs)
		if !ok {
			continue
		}
		if best == nil || moreSpecific(cand.pattern, best.pattern) {
			best, params = cand, p
		}
	}
	return best, params
}

// runGuards runs the route's guards in order. On the first non-allow verdict
// it writes the blocking response and returns false.
func (a *App) runGuards(w http.ResponseWriter, r *http.Request, rt *route, ctx Ctx) bool {
	for _, g := range rt.guards {
		res := g(ctx)
		if res.allowed {
			continue
		}
		if res.redirectTo != "" {
			http.Redirect(w, r, res.redirectTo, http.StatusFound)
			return false
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// renderRoute runs the component lifecycle on a fresh instance — param
// binding, OnInit, buffered render — and writes the result.
func (a *App) renderRoute(w http.ResponseWriter, r *http.Request, rt *route, params map[string]string, ctx Ctx) {
	inst := reflect.New(rt.prototype.Elem().Type())
	inst.Elem().Set(rt.prototype.Elem())
	for _, inj := range rt.injections {
		inst.Elem().Field(inj.field).Set(inj.svc)
	}
	bindPathParams(inst.Elem(), params)

	// A component is session-bound when it has hydro state to register or a
	// CSRF token to carry; either way the render mints the session, a fresh
	// CSRF token for the runtime's event payloads (D15), and — for hydro
	// components — the patch-boundary token.
	var sessionID, hydroID, csrf string
	if rt.hydroField >= 0 || rt.csrfField >= 0 {
		var err error
		if sessionID, hydroID, csrf, err = a.mintRenderTokens(w, r, rt); err != nil {
			a.logger.Error("establishing hydro session", "path", r.URL.Path, "error", err)
			a.errorPage(w)
			return
		}
		ctx.session = sessionID
	}
	if rt.hydroField >= 0 {
		inst.Elem().Field(rt.hydroField).SetString(hydroID)
	}
	if rt.csrfField >= 0 {
		inst.Elem().Field(rt.csrfField).SetString(csrf)
	}

	if init, ok := inst.Interface().(Initializer); ok {
		if err := init.OnInit(ctx); err != nil {
			a.logger.Error("initializing component", "path", r.URL.Path, "error", err)
			a.errorPage(w)
			return
		}
	}

	var buf bytes.Buffer
	if err := rt.tmpl.Execute(&buf, inst.Interface()); err != nil {
		a.logger.Error("rendering component", "path", r.URL.Path, "error", err)
		a.errorPage(w)
		return
	}
	if rt.hydroField >= 0 {
		a.hydro.put(sessionID, hydroID, &hydroState{inst: inst, rt: rt})
	}

	head := rt.fallbackHead
	if hp, ok := inst.Interface().(HeadProvider); ok {
		head = hp.Head()
	}
	var page bytes.Buffer
	if err := shellTmpl.Execute(&page, shellData{Head: head, CSRF: csrf, Body: template.HTML(buf.String())}); err != nil {
		a.logger.Error("rendering document shell", "path", r.URL.Path, "error", err)
		a.errorPage(w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := page.WriteTo(w); err != nil {
		a.logger.Error("writing response", "path", r.URL.Path, "error", err)
	}
}
