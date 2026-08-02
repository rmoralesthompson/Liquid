package liquid

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"
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
// and mutated by dispatched events. mu serializes dispatch: events for the
// same instance never run concurrently (D20).
type hydroState struct {
	mu   sync.Mutex
	inst reflect.Value
	rt   *route
}

// The registry's hard caps: unauthenticated traffic must not grow it without
// limit, so both dimensions evict oldest-first at a fixed bound. Configurable
// caps, LRU ordering, idle-timeout GC, and request body limits are session
// hardening, tracked on ticket #9.
const (
	maxSessions        = 1024
	maxHydroPerSession = 64
)

// hydroSession is one browser session's live instances, in insertion order
// so the oldest is evicted at the cap.
type hydroSession struct {
	states map[string]*hydroState
	order  []string
}

// hydroRegistry maps sessionID → hydroID → live instance (D15). The zero
// value is ready to use.
type hydroRegistry struct {
	mu       sync.Mutex
	sessions map[string]*hydroSession
	order    []string // session insertion order, oldest first
}

// put registers a live instance under its session and hydro tokens, evicting
// the oldest session or entry when a cap is reached.
func (h *hydroRegistry) put(sessionID, hydroID string, st *hydroState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = make(map[string]*hydroSession)
	}
	sess := h.sessions[sessionID]
	if sess == nil {
		if len(h.order) >= maxSessions {
			delete(h.sessions, h.order[0])
			h.order = h.order[1:]
		}
		sess = &hydroSession{states: make(map[string]*hydroState)}
		h.sessions[sessionID] = sess
		h.order = append(h.order, sessionID)
	}
	if _, exists := sess.states[hydroID]; !exists {
		if len(sess.order) >= maxHydroPerSession {
			delete(sess.states, sess.order[0])
			sess.order = sess.order[1:]
		}
		sess.order = append(sess.order, hydroID)
	}
	sess.states[hydroID] = st
}

// get returns the live instance for a session/hydro token pair, or nil.
func (h *hydroRegistry) get(sessionID, hydroID string) *hydroState {
	h.mu.Lock()
	defer h.mu.Unlock()
	sess := h.sessions[sessionID]
	if sess == nil {
		return nil
	}
	return sess.states[hydroID]
}

// has reports whether sessionID is a live, server-minted session key.
func (h *hydroRegistry) has(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.sessions[sessionID]
	return ok
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
	if ck, err := r.Cookie(sessionCookieName); err == nil && ck.Value != "" && a.hydro.has(ck.Value) {
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
	f, ok := t.FieldByName("HydroID")
	if ok && f.Type.Kind() == reflect.String && len(f.Index) == 1 {
		return f.Index[0]
	}
	return -1
}

// resolveActions maps a component's compiled allowlist to method indexes,
// once, at registration. Dispatch later selects among these precomputed
// entries — it never reflects on a client-supplied name. A component whose
// allowlist names a missing or mis-shaped method breaks the compiler contract
// and fails registration loudly.
func resolveActions(v reflect.Value) (map[string]int, error) {
	ap, ok := v.Interface().(ActionProvider)
	if !ok {
		return nil, nil
	}
	structName := v.Elem().Type().Name()
	if hydroIDField(v.Elem().Type()) < 0 {
		return nil, fmt.Errorf("component %s declares actions but has no HydroID string field to patch against", structName)
	}
	actions := make(map[string]int)
	for _, name := range ap.Actions() {
		m, ok := v.Type().MethodByName(name)
		if !ok {
			return nil, fmt.Errorf("allowlisted action %s has no method on %s", name, structName)
		}
		if m.Type.NumIn() != 1 || m.Type.NumOut() != 0 {
			return nil, fmt.Errorf("allowlisted action %s.%s must have signature func(), got %s", structName, name, m.Type)
		}
		actions[name] = m.Index
	}
	return actions, nil
}

// Envelope is the hydro event response (D19): an HTML patch to swap at the
// [hydroId] boundary, or a redirect for the runtime to navigate to.
type Envelope struct {
	Patch    string `json:"patch,omitempty"`
	Redirect string `json:"redirect,omitempty"`
}

// hydroEvent is the payload the runtime script posts.
type hydroEvent struct {
	HydroID string `json:"hydroId"`
	Action  string `json:"action"`
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
	var ev hydroEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "malformed event payload", http.StatusBadRequest)
		return
	}
	ck, err := r.Cookie(sessionCookieName)
	if err != nil || ck.Value == "" {
		http.NotFound(w, r)
		return
	}
	st := a.hydro.get(ck.Value, ev.HydroID)
	if st == nil {
		http.NotFound(w, r)
		return
	}
	idx, ok := st.rt.actions[ev.Action]
	if !ok {
		http.NotFound(w, r)
		return
	}

	st.mu.Lock()
	st.inst.Method(idx).Call(nil)
	var buf bytes.Buffer
	renderErr := st.rt.tmpl.Execute(&buf, st.inst.Interface())
	st.mu.Unlock()
	if renderErr != nil {
		a.logger.Error("rendering event patch", "action", ev.Action, "error", renderErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(Envelope{Patch: buf.String()}); err != nil {
		a.logger.Error("writing event envelope", "error", err)
	}
}
