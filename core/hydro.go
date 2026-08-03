package liquid

import (
	"container/list"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"
)

// hydroEventPath is the fixed endpoint the runtime script posts events to.
const hydroEventPath = "/hydro-event"

// runtimeScriptPath is where every page loads the fixed runtime script from.
// It lives under /liquid/, apart from the user-owned /static/ mount, and is
// served as a plain file so pages need no inline JS (D24).
const runtimeScriptPath = "/liquid/runtime.js"

//go:embed runtime.js
var runtimeScript []byte

// serveRuntimeScript writes the embedded runtime script.
func (a *App) serveRuntimeScript(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if _, err := w.Write(runtimeScript); err != nil {
		a.logger.Error("writing runtime script", "error", err)
	}
}

// sessionCookieName is the browser-session cookie carrying the opaque session
// ID that hydro tokens nest under (D15).
const sessionCookieName = "liquid_session"

// ActionProvider is implemented by interactive components; liquid build
// generates Actions from the template's event bindings (D10). The server
// dispatches only these — a method absent from the list does not exist as far
// as the event endpoint is concerned.
type ActionProvider interface {
	Actions() []string
}

// hydroState is one live interactive component instance, registered at render
// and mutated by dispatched events. Dispatch is serialized by the owning
// session's mutex, not per instance (D20).
type hydroState struct {
	inst reflect.Value
	reg  *registration
	// subs are the instance's declared subject bindings. Every render of the
	// instance — event dispatch or pump push — first applies them under the
	// dispatch mutex, so a handler that emits to a subject it observes sees
	// the emission in its own response patch.
	subs []Subscription
	// ready reports whether the instance may be dispatched against. True from
	// registration for an inline instance; a deferred instance (#26) starts
	// false and its background load flips it true — under the session's
	// dispatch mutex, at the same instant it becomes live — so the load never
	// mutates the instance concurrently with a dispatched handler (D20.1).
	// Read and written only under the owning session's dispatch mutex.
	ready bool
}

// renderStateLocked applies the instance's subject bindings and renders it —
// the one sequence every re-render (event dispatch or pump push) goes
// through, so both always reflect current subject state. The render scope
// carries the owning session, so nested children re-render (and re-register,
// for interactive ones) inside the patch (D14). Callers hold the owning
// session's dispatch mutex (D20.1).
func (a *App) renderStateLocked(st *hydroState, sessionID string) (string, error) {
	for _, sub := range st.subs {
		sub.apply()
	}
	sc := &renderScope{a: a, ensure: func() (string, error) { return sessionID, nil }}
	return a.executeComponent(st.reg, st.inst, sc)
}

// The registry's default caps: unauthenticated traffic must not grow it
// without limit, so both dimensions evict at a bound even when the app
// configures nothing (D20).
const (
	// DefaultMaxSessions is the Limits.MaxSessions default.
	DefaultMaxSessions = 1024
	// DefaultMaxComponentsPerSession is the Limits.MaxComponentsPerSession
	// default.
	DefaultMaxComponentsPerSession = 64
	// DefaultSessionIdleTimeout is the Limits.SessionIdleTimeout default.
	DefaultSessionIdleTimeout = time.Hour
	// DefaultMaxEventBytes is the Limits.MaxEventBytes default: 64 KiB.
	DefaultMaxEventBytes = 64 << 10
	// DefaultMaxStreamsPerSession is the Limits.MaxStreamsPerSession
	// default: enough for a handful of tabs, small enough that a
	// cookie-holder cannot pin goroutines without bound.
	DefaultMaxStreamsPerSession = 8
)

// Limits bounds the in-memory session registry (D20). The registry is always
// bounded: a zero or negative field means its documented default, and there
// is no unlimited setting.
type Limits struct {
	// MaxSessions caps live sessions across the App. At the cap, minting a
	// new session evicts the oldest. Default DefaultMaxSessions.
	MaxSessions int
	// MaxComponentsPerSession caps live component instances under one
	// session. At the cap, registering a new instance evicts the session's
	// oldest. Default DefaultMaxComponentsPerSession.
	MaxComponentsPerSession int
	// SessionIdleTimeout is how long a session may go without a request
	// before it expires and its live instances are dropped (D2). CSRF token
	// expiry tracks the same window (D15). Default
	// DefaultSessionIdleTimeout.
	SessionIdleTimeout time.Duration
	// MaxEventBytes caps the /hydro-event request body, enforced while the
	// body is read — an oversized event is refused with 413 without being
	// parsed. Default DefaultMaxEventBytes.
	MaxEventBytes int64
	// MaxStreamsPerSession caps open SSE connections under one session. At
	// the cap, a new connection disconnects the session's oldest — the
	// dropped browser reconnects into a full re-render (D20). Default
	// DefaultMaxStreamsPerSession.
	MaxStreamsPerSession int
}

// withDefaults fills unset (zero or negative) fields with their documented
// defaults.
func (l Limits) withDefaults() Limits {
	if l.MaxSessions <= 0 {
		l.MaxSessions = DefaultMaxSessions
	}
	if l.MaxComponentsPerSession <= 0 {
		l.MaxComponentsPerSession = DefaultMaxComponentsPerSession
	}
	if l.SessionIdleTimeout <= 0 {
		l.SessionIdleTimeout = DefaultSessionIdleTimeout
	}
	if l.MaxEventBytes <= 0 {
		l.MaxEventBytes = DefaultMaxEventBytes
	}
	if l.MaxStreamsPerSession <= 0 {
		l.MaxStreamsPerSession = DefaultMaxStreamsPerSession
	}
	return l
}

// hydroEntry is one live instance under its hydro token — the unit the
// per-session LRU orders.
type hydroEntry struct {
	id string
	st *hydroState
	// stop tears down the instance's subscription pump, nil when the
	// component observes nothing. It runs (idempotently) wherever the entry
	// leaves the registry — subscriptions live exactly as long as their
	// entry (D20).
	stop func()
	// prime nudges the pump to push the instance's current state, nil when
	// there is no pump. A connecting stream runs every prime in its session
	// so the client starts from current state — the connect-time half of
	// D20's "full re-render, no replay".
	prime func()
}

// stopPump runs the entry's teardown, if it has one.
func (e *hydroEntry) stopPump() {
	if e.stop != nil {
		e.stop()
	}
}

// hydroSession is one browser session's live instances, evicting the least
// recently used at the cap (D20).
type hydroSession struct {
	id         string
	lastActive time.Time                // last request touching this session; idle expiry keys off it
	entries    map[string]*list.Element // hydroID → element in lru
	lru        *list.List               // of *hydroEntry; front is the eviction candidate
	dispatch   sync.Mutex               // serializes event dispatch for the whole session (D20.1); lock order is one-way: dispatch may be held while taking the registry's mu (a re-render registering a nested child does), never the reverse
	streams    []*sseStream             // open SSE connections, oldest first; bounded, closed when the session goes (D3/D20)
}

// shutdown disconnects everything tied to the session's lifetime: every
// entry's subscription pump and every open SSE stream. Called wherever a
// session leaves the registry, with the registry's mu held.
func (s *hydroSession) shutdown() {
	for elem := s.lru.Front(); elem != nil; elem = elem.Next() {
		elem.Value.(*hydroEntry).stopPump()
	}
	for _, stream := range s.streams {
		stream.close()
	}
}

// touchEntry returns hydroID's live entry marked most recently used, or nil.
// Callers hold the registry's mu.
func (s *hydroSession) touchEntry(hydroID string) *hydroEntry {
	elem, ok := s.entries[hydroID]
	if !ok {
		return nil
	}
	s.lru.MoveToBack(elem)
	return elem.Value.(*hydroEntry)
}

// putEntry registers a live instance under its hydro token, evicting the
// session's least recently used entry at the cap.
func (s *hydroSession) putEntry(hydroID string, st *hydroState, limit int) {
	if e := s.touchEntry(hydroID); e != nil {
		e.stopPump()
		e.st, e.stop, e.prime = st, nil, nil
		return
	}
	if s.lru.Len() >= limit {
		evict := s.lru.Remove(s.lru.Front()).(*hydroEntry)
		delete(s.entries, evict.id)
		evict.stopPump()
	}
	s.entries[hydroID] = s.lru.PushBack(&hydroEntry{id: hydroID, st: st})
}

// hydroRegistry maps sessionID → hydroID → live instance (D15), evicting the
// least recently used session at the cap (D20). The zero value is ready to
// use.
type hydroRegistry struct {
	mu       sync.Mutex
	sessions map[string]*list.Element // sessionID → element in lru
	lru      *list.List               // of *hydroSession; front is the eviction candidate
}

// touchSession returns sessionID's live session marked most recently used,
// or nil. A session idle past the window is expired here — removed, not
// returned (D2). Callers hold h.mu.
func (h *hydroRegistry) touchSession(sessionID string, now time.Time, idle time.Duration) *hydroSession {
	elem, ok := h.sessions[sessionID]
	if !ok {
		return nil
	}
	sess := elem.Value.(*hydroSession)
	if now.Sub(sess.lastActive) > idle {
		h.lru.Remove(elem)
		delete(h.sessions, sessionID)
		sess.shutdown()
		return nil
	}
	sess.lastActive = now
	h.lru.MoveToBack(elem)
	return sess
}

// expireIdle removes every session idle past the window. Touches keep the
// LRU list ordered by lastActive, so expired sessions are exactly the list's
// front run. Callers hold h.mu.
func (h *hydroRegistry) expireIdle(now time.Time, idle time.Duration) {
	if h.lru == nil {
		return
	}
	for front := h.lru.Front(); front != nil; front = h.lru.Front() {
		sess := front.Value.(*hydroSession)
		if now.Sub(sess.lastActive) <= idle {
			return
		}
		h.lru.Remove(front)
		delete(h.sessions, sess.id)
		sess.shutdown()
	}
}

// put registers a live instance under its session and hydro tokens,
// returning the owning session (whose dispatch mutex and streams a
// subscription pump rides on). Idle sessions are swept first; then a cap
// breach evicts the least recently used session or the session's oldest
// entry.
func (h *hydroRegistry) put(sessionID, hydroID string, st *hydroState, now time.Time, limits Limits) *hydroSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = make(map[string]*list.Element)
		h.lru = list.New()
	}
	h.expireIdle(now, limits.SessionIdleTimeout)
	sess := h.touchSession(sessionID, now, limits.SessionIdleTimeout)
	if sess == nil {
		if h.lru.Len() >= limits.MaxSessions {
			evict := h.lru.Remove(h.lru.Front()).(*hydroSession)
			delete(h.sessions, evict.id)
			evict.shutdown()
		}
		sess = &hydroSession{id: sessionID, lastActive: now, entries: make(map[string]*list.Element), lru: list.New()}
		h.sessions[sessionID] = h.lru.PushBack(sess)
	}
	sess.putEntry(hydroID, st, limits.MaxComponentsPerSession)
	return sess
}

// attachPump wires a freshly started pump — its teardown and its
// current-state prime — to its registry entry, reporting false when the
// entry was already evicted in the window since put; the caller must then
// run stop itself, since no eviction path ever will.
func (h *hydroRegistry) attachPump(sessionID, hydroID string, stop, prime func()) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	elem, ok := h.sessions[sessionID]
	if !ok {
		return false
	}
	sess := elem.Value.(*hydroSession)
	entry, ok := sess.entries[hydroID]
	if !ok {
		return false
	}
	e := entry.Value.(*hydroEntry)
	e.stop, e.prime = stop, prime
	return true
}

// get returns the live instance for a session/hydro token pair along with
// its owning session (whose dispatch mutex the caller serializes on), or
// nils. A hit marks both the session and the entry most recently used; an
// idle-expired session is removed and misses.
func (h *hydroRegistry) get(sessionID, hydroID string, now time.Time, idle time.Duration) (*hydroState, *hydroSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sess := h.touchSession(sessionID, now, idle)
	if sess == nil {
		return nil, nil
	}
	e := sess.touchEntry(hydroID)
	if e == nil {
		return nil, nil
	}
	return e.st, sess
}

// touch reports whether sessionID is a live, server-minted session key.
// Being presented by a request counts as use: a hit marks the session most
// recently used; an idle-expired session is removed and misses.
func (h *hydroRegistry) touch(sessionID string, now time.Time, idle time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.touchSession(sessionID, now, idle) != nil
}

// randomToken returns a cryptographically random opaque token. Tokens carry
// no meaning — never a memory address or anything derived from one.
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ensureSession returns the request's liquid_session ID, minting the cookie
// on the response when the browser has none yet (D15). A cookie value is
// adopted only when it is a live registry key — i.e. an ID this server
// minted — so an expired, evicted, or attacker-chosen value gets a fresh ID
// instead of becoming a registry key.
func (a *App) ensureSession(w http.ResponseWriter, r *http.Request) (string, error) {
	if ck, err := r.Cookie(sessionCookieName); err == nil && ck.Value != "" && a.hydro.touch(ck.Value, a.now(), a.limits.SessionIdleTimeout) {
		return ck.Value, nil
	}
	id, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("minting session ID: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return id, nil
}

// hydroIDField returns the index of a component type's HydroID string field,
// or -1 for a non-interactive component.
func hydroIDField(t reflect.Type) int {
	return stringFieldIndex(t, "HydroID")
}

// stringFieldIndex returns the index of t's own (non-promoted) string field
// with the given name, or -1 — the shape shared by the framework-populated
// HydroID and CSRFToken fields.
func stringFieldIndex(t reflect.Type, name string) int {
	f, ok := t.FieldByName(name)
	if ok && f.Type.Kind() == reflect.String && len(f.Index) == 1 {
		return f.Index[0]
	}
	return -1
}

// action is one dispatchable allowlist entry: the handler's method index and
// whether it takes the liquid.Event payload (D11), both resolved once at
// registration.
type action struct {
	idx        int
	takesEvent bool
}

// eventType is the reflect shape of the liquid.Event handler parameter.
var eventType = reflect.TypeFor[Event]()

// resolveActions maps a component's compiled allowlist to method indexes,
// once, at registration. Dispatch later selects among these precomputed
// entries — it never reflects on a client-supplied name. A component whose
// allowlist names a missing or mis-shaped method breaks the compiler contract
// and fails registration loudly.
func resolveActions(v reflect.Value) (map[string]action, error) {
	ap, ok := v.Interface().(ActionProvider)
	if !ok {
		return nil, nil
	}
	structName := v.Elem().Type().Name()
	if hydroIDField(v.Elem().Type()) < 0 {
		return nil, fmt.Errorf("component %s declares actions but has no HydroID string field to patch against", structName)
	}
	actions := make(map[string]action)
	for _, name := range ap.Actions() {
		m, ok := v.Type().MethodByName(name)
		if !ok {
			return nil, fmt.Errorf("allowlisted action %s has no method on %s", name, structName)
		}
		takesEvent := m.Type.NumIn() == 2 && m.Type.In(1) == eventType
		if m.Type.NumOut() != 0 || (m.Type.NumIn() != 1 && !takesEvent) {
			return nil, fmt.Errorf("allowlisted action %s.%s must have signature func() or func(e liquid.Event), got %s",
				structName, name, m.Type)
		}
		actions[name] = action{idx: m.Index, takesEvent: takesEvent}
	}
	return actions, nil
}

// Envelope is the hydro event response (D19): an HTML patch to swap at the
// [hydroId] boundary, or a redirect for the runtime to navigate to. A patch
// answer also re-mints the render's CSRF token (D15, #46) so a long-open
// interactive page tracks the session's sliding idle deadline instead of the
// original render's fixed horizon; it is empty on a redirect answer.
type Envelope struct {
	Patch    string `json:"patch,omitempty"`
	Redirect string `json:"redirect,omitempty"`
	CSRF     string `json:"csrf,omitempty"`
}

// hydroEvent is the payload the runtime script posts.
type hydroEvent struct {
	HydroID   string            `json:"hydroId"`
	Action    string            `json:"action"`
	Payload   map[string]string `json:"payload"`
	CSRFToken string            `json:"csrfToken"`
}

// serveHydroEvent dispatches one posted event: resolve the live instance
// under the session cookie, check the action against the route's precomputed
// allowlist (unknown anything is a 404), invoke the handler, and respond with
// the re-rendered component subtree as a patch envelope — the component
// render only, never the document shell (D14). Events for one instance are
// serialized by its mutex (D20).
func (a *App) serveHydroEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The body is bounded before it is read: a declared oversize is refused
	// outright, and MaxBytesReader stops a lying or chunked sender at the
	// cap mid-decode (D20).
	if r.ContentLength > a.limits.MaxEventBytes {
		http.Error(w, "event payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.limits.MaxEventBytes)
	var ev hydroEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "event payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "malformed event payload", http.StatusBadRequest)
		return
	}
	ck, err := r.Cookie(sessionCookieName)
	if err != nil || ck.Value == "" {
		http.NotFound(w, r)
		return
	}
	// CSRF comes before anything the event names: a request that cannot
	// prove it originated from a page this server rendered for this session
	// learns nothing about tokens or actions (D15).
	if !validCSRF(a.csrfSecret, ev.CSRFToken, ck.Value, a.now()) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return
	}
	st, sess := a.hydro.get(ck.Value, ev.HydroID, a.now(), a.limits.SessionIdleTimeout)
	if st == nil {
		http.NotFound(w, r)
		return
	}
	act, ok := st.reg.actions[ev.Action]
	if !ok {
		http.NotFound(w, r)
		return
	}

	reply := &eventReply{}
	var args []reflect.Value
	if act.takesEvent {
		args = []reflect.Value{reflect.ValueOf(Event{
			Ctx:    NewCtx(r, nil),
			fields: ev.Payload,
			reply:  reply,
		})}
	}

	sess.dispatch.Lock()
	// A deferred instance whose background load has not published yet is not
	// live: its OnInit is still mutating it off-mutex, so dispatching now
	// would race that write (D20.1). It reads as absent until ready.
	if !st.ready {
		sess.dispatch.Unlock()
		http.NotFound(w, r)
		return
	}
	st.inst.Method(act.idx).Call(args)
	env := Envelope{Redirect: reply.redirect}
	var renderErr error
	if env.Redirect == "" {
		// Only a patch answer needs the re-render (D19); renderStateLocked
		// makes it reflect any subject emission the handler just made. The
		// same answer re-mints the CSRF token against the current clock, so
		// its expiry tracks the session's sliding idle deadline — which
		// dispatching this event just extended — rather than the original
		// render's fixed horizon (D15/D2, #46); the runtime restamps it into
		// the liquid-csrf meta tag and the swapped subtree's csrf_token
		// inputs. A redirect answer navigates away, so it carries no token.
		env.Patch, renderErr = a.renderStateLocked(st, ck.Value)
		env.CSRF = mintCSRF(a.csrfSecret, ck.Value, a.limits.SessionIdleTimeout, a.now())
	}
	sess.dispatch.Unlock()
	if renderErr != nil {
		a.logger.Error("rendering event patch", "action", ev.Action, "error", renderErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(env); err != nil {
		a.logger.Error("writing event envelope", "error", err)
	}
}
