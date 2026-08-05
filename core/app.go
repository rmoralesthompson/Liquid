package liquid

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// App routes HTTP requests to components. Component templates are parsed
// once, at registration; every request renders a fresh component instance —
// registered instances act only as prototypes and are never shared mutable
// state across requests.
type App struct {
	logger     *slog.Logger
	routes     []*route
	components map[string]*registration // selector → render machinery; child selectors resolve here (D14)
	services   []reflect.Value          // Provide'd singletons, in registration order
	static     http.Handler             // file server mounted at /static/, nil until Static
	hydro      hydroRegistry            // live interactive instances (D15)
	csrfSecret []byte                   // HMAC key for CSRF tokens, minted per process (D15)
	limits     Limits                   // registry and request bounds, defaults applied (D20)
	now        func() time.Time         // the App's clock; crypto real time in prod, pinnable in tests for idle-expiry and deterministic-render control (D28)
	rand       io.Reader                // opaque-token CSPRNG source (D15); crypto/rand.Reader in every build, replaceable only in-package for deterministic render (D28)
	dev        devState                 // dev-build broadcaster (D16); an empty struct in production builds

	insecureWarn sync.Once // fires the plain-HTTP-non-localhost Secure-cookie warning at most once (#47)
}

// registration is one component type's render machinery, resolved once when
// the component is first registered (via Route or Register) and shared by
// every occurrence — routed pages and nested child renders alike.
type registration struct {
	selector    string
	prototype   reflect.Value // pointer to the registered component struct
	tmplText    string
	tmpl        *template.Template
	injections  []injection       // dependencies resolved at registration (D8)
	hydroField  int               // index of the HydroID field; -1 when not interactive
	csrfField   int               // index of the CSRFToken field; -1 when the component has none
	actions     map[string]action // allowlisted action → dispatch shape, resolved at registration (D10)
	hasChildren bool              // template nests child selectors or defers one; renders bind liquidChild/liquidDefer to a scope
}

// newInstance returns a fresh component instance seeded from the prototype
// with its registered dependencies injected — never a shared one (per-request
// invariant).
func (reg *registration) newInstance() reflect.Value {
	inst := reflect.New(reg.prototype.Elem().Type())
	inst.Elem().Set(reg.prototype.Elem())
	for _, inj := range reg.injections {
		inst.Elem().Field(inj.field).Set(inj.svc)
	}
	return inst
}

type route struct {
	pattern      []string // path segments; a ":name" segment binds a param
	reg          *registration
	guards       []Guard
	fallbackHead Head // shell head for components without HeadProvider
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

// WithLimits sets the App's session-registry and request bounds (D20).
// Unset (zero) fields keep their documented defaults; without this option
// every limit is at its default.
func WithLimits(l Limits) Option {
	return func(a *App) { a.limits = l.withDefaults() }
}

// New creates an App, applying any options.
func New(opts ...Option) *App {
	a := &App{
		logger: slog.Default(),
		limits: Limits{}.withDefaults(),
		now:    time.Now,
		rand:   rand.Reader,
	}
	for _, opt := range opts {
		opt(a)
	}
	// The CSRF secret is drawn from the App's token source after options
	// apply, so a deterministic-render build (D28) that swapped the source
	// gets a reproducible secret too — production keeps crypto/rand.Reader,
	// unchanged from a direct rand.Read.
	secret := make([]byte, 32)
	if _, err := io.ReadFull(a.rand, secret); err != nil {
		// No entropy at construction is unrecoverable and must not fall
		// through to serving unsigned tokens.
		panic(fmt.Sprintf("liquid: generating CSRF secret: %v", err))
	}
	a.csrfSecret = secret
	a.initDev()
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
	reg, err := a.register(c)
	if err != nil {
		return fmt.Errorf("registering route %s: %w", path, err)
	}
	if err := validatePathParamTags(reg.prototype.Elem().Type()); err != nil {
		return fmt.Errorf("registering route %s: %w", path, err)
	}

	rt := &route{
		pattern:      splitPath(path),
		reg:          reg,
		fallbackHead: Head{Title: c.Selector()},
	}
	for _, opt := range opts {
		opt(rt)
	}
	a.routes = append(a.routes, rt)
	return nil
}

// Register adds a component to the App's registry so parent templates can
// nest it by selector (D14). Routing a component registers it too; Register
// is for components that only ever render as children. Registering the same
// component type again is a no-op; a selector claimed by a different type is
// an error.
func (a *App) Register(c Component) error {
	if _, err := a.register(c); err != nil {
		return fmt.Errorf("registering component %T: %w", c, err)
	}
	return nil
}

// register resolves a component's render machinery once and stores it in the
// selector registry. The template is parsed here, at registration — a
// template error is reported now, never at request time.
func (a *App) register(c Component) (*registration, error) {
	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("component must be a pointer to a struct, got %T", c)
	}
	selector := c.Selector()
	if existing, ok := a.components[selector]; ok {
		if existing.prototype.Elem().Type() == v.Elem().Type() {
			return existing, nil
		}
		return nil, fmt.Errorf("selector %s is already registered by component %s",
			selector, existing.prototype.Elem().Type().Name())
	}
	if err := validatePrototype(v.Elem(), v.Elem().Type().Name()); err != nil {
		return nil, err
	}

	injections, err := a.resolveInjections(v.Elem().Type())
	if err != nil {
		return nil, err
	}
	actions, err := resolveActions(v)
	if err != nil {
		return nil, err
	}
	if _, ok := c.(SubscriptionProvider); ok && hydroIDField(v.Elem().Type()) < 0 {
		return nil, fmt.Errorf("component %s declares subscriptions but has no HydroID string field to push patches against",
			v.Elem().Type().Name())
	}

	tmplText := c.Template()
	tmpl, err := template.New(selector).Funcs(registrationStubFuncs).Parse(tmplText)
	if err != nil {
		return nil, fmt.Errorf("parsing template for %s: %w", selector, err)
	}

	reg := &registration{
		selector:    selector,
		prototype:   v,
		tmplText:    tmplText,
		tmpl:        tmpl,
		injections:  injections,
		hydroField:  hydroIDField(v.Elem().Type()),
		csrfField:   csrfTokenField(v.Elem().Type()),
		actions:     actions,
		hasChildren: tmpl.Tree != nil && usesRenderScope(tmpl.Tree.Root),
	}
	if a.components == nil {
		a.components = make(map[string]*registration)
	}
	a.components[selector] = reg
	return reg, nil
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

// errorPage writes the framework error page as a 500 response. detail is the
// underlying diagnostic: dev builds render it on the page (D18's dev half),
// production builds ignore it — there the error lives only in the log.
func (a *App) errorPage(w http.ResponseWriter, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte(errorPageBody(detail))); err != nil {
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
		a.errorPage(w, fmt.Sprintf("panic: %v\n\n%s", rec, debug.Stack()))
	}()

	if r.URL.Path == hydroEventPath {
		a.serveHydroEvent(w, r)
		return
	}
	if r.URL.Path == hydroSSEPath {
		a.serveHydroSSE(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.serveScript(w, r) {
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

// serveScript handles the framework's fixed script paths: the runtime script
// in every build, plus the dev script in dev builds only.
func (a *App) serveScript(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == runtimeScriptPath {
		a.serveRuntimeScript(w)
		return true
	}
	if devMode && r.URL.Path == devScriptPath {
		a.serveDevScript(w)
		return true
	}
	return false
}

// registerHydro puts a rendered interactive instance in the session
// registry and, for a SubscriptionProvider, activates its subject bindings:
// the pump starts and its teardown is wired to the registry entry, so the
// subscriptions live exactly as long as the entry (D20).
func (a *App) registerHydro(sessionID, hydroID string, inst reflect.Value, reg *registration) {
	var subs []Subscription
	if sp, ok := inst.Interface().(SubscriptionProvider); ok {
		subs = sp.Subscriptions()
	}
	st := &hydroState{inst: inst, reg: reg, subs: subs, ready: true}
	sess := a.hydro.put(sessionID, hydroID, st, a.now(), a.limits)
	if len(subs) == 0 {
		return
	}
	stop, prime := a.startPump(sess, st, hydroID)
	if !a.hydro.attachPump(sessionID, hydroID, stop, prime) {
		// The entry was evicted in the window since put; no eviction path
		// will ever run this stop, so it runs here.
		stop()
	}
}

// mintRenderTokens establishes one session-bound render's values: the
// session ID, a fresh hydro token when the component is interactive, and
// the render's CSRF token (D15).
func (a *App) mintRenderTokens(w http.ResponseWriter, r *http.Request, reg *registration) (sessionID, hydroID, csrf string, err error) {
	if sessionID, err = a.ensureSession(w, r); err == nil && reg.hydroField >= 0 {
		hydroID, err = a.newToken()
	}
	if err != nil {
		return "", "", "", err
	}
	return sessionID, hydroID, mintCSRF(a.csrfSecret, sessionID, a.limits.SessionIdleTimeout, a.now()), nil
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
	reg := rt.reg
	inst := reg.newInstance()
	bindPathParams(inst.Elem(), params)

	// A component is session-bound when it has hydro state to register or a
	// CSRF token to carry; either way the render mints the session, a fresh
	// CSRF token for the runtime's event payloads (D15), and — for hydro
	// components — the patch-boundary token. A nested child may also bind the
	// render to a session (its own hydro or CSRF plumbing), so the scope
	// establishes the session lazily on first need; the render is buffered,
	// so the cookie can still be set then.
	var sessionID, hydroID, csrf string
	ensure := func() (string, error) {
		if sessionID != "" {
			return sessionID, nil
		}
		var err error
		sessionID, err = a.ensureSession(w, r)
		return sessionID, err
	}
	if reg.hydroField >= 0 || reg.csrfField >= 0 {
		var err error
		if sessionID, hydroID, csrf, err = a.mintRenderTokens(w, r, reg); err != nil {
			a.logger.Error("establishing hydro session", "path", r.URL.Path, "error", err)
			a.errorPage(w, err.Error())
			return
		}
		ctx.session = sessionID
	}
	if reg.hydroField >= 0 {
		inst.Elem().Field(reg.hydroField).SetString(hydroID)
	}
	if reg.csrfField >= 0 {
		inst.Elem().Field(reg.csrfField).SetString(csrf)
	}

	if init, ok := inst.Interface().(Initializer); ok {
		if err := init.OnInit(ctx); err != nil {
			a.logger.Error("initializing component", "path", r.URL.Path, "error", err)
			a.errorPage(w, err.Error())
			return
		}
	}

	body, err := a.executeComponent(reg, inst, &renderScope{a: a, ensure: ensure, reqCtx: ctx})
	if err != nil {
		a.logger.Error("rendering component", "path", r.URL.Path, "error", err)
		a.errorPage(w, err.Error())
		return
	}
	if reg.hydroField >= 0 {
		a.registerHydro(sessionID, hydroID, inst, reg)
	}
	// A child render may have bound the page to a session the route's own
	// component never asked for; the shell still needs that render's CSRF
	// token so the runtime script can authenticate the child's events (D15).
	if csrf == "" && sessionID != "" {
		csrf = mintCSRF(a.csrfSecret, sessionID, a.limits.SessionIdleTimeout, a.now())
	}
	head := rt.fallbackHead
	if hp, ok := inst.Interface().(HeadProvider); ok {
		head = hp.Head()
	}
	a.writeShell(w, r.URL.Path, head, csrf, body)
}

// writeShell wraps a rendered component body in the document shell and
// writes the complete page.
func (a *App) writeShell(w http.ResponseWriter, path string, head Head, csrf, body string) {
	var page bytes.Buffer
	if err := shellTmpl.Execute(&page, shellData{Head: head, CSRF: csrf, Body: template.HTML(body), Dev: devShellScript}); err != nil {
		a.logger.Error("rendering document shell", "path", path, "error", err)
		a.errorPage(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := page.WriteTo(w); err != nil {
		a.logger.Error("writing response", "path", path, "error", err)
	}
}
