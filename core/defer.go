package liquid

import (
	"context"
	"fmt"
	"html"
	"reflect"
	"sync"
)

// deferChild is the render-scope side of a *liquidDefer occurrence (#26). It
// runs during the parent's render, synchronously, and does not render the
// child: it mints the patch-boundary token, reserves the child's registry
// entry, and spawns the background load, then returns the token for the
// fallback slot's data-hydro-id. The real content arrives later as a pushed
// swap frame. args alternate [input] field name and value, exactly as for an
// inline child.
func (sc *renderScope) deferChild(selector string, args ...any) (string, error) {
	reg, ok := sc.a.components[selector]
	if !ok {
		return "", fmt.Errorf("no component registered for selector %s; App.Register it before routing its parents", selector)
	}
	// LSX016 rejects a deferred component without a HydroID field at build;
	// this is the runtime backstop, so a hand-written template cannot defer
	// against a component with no boundary to swap at.
	if reg.hydroField < 0 {
		return "", fmt.Errorf("deferring %s: component has no HydroID field for the completion patch to target", selector)
	}

	inst := reg.newInstance()
	if err := applyChildInputs(inst.Elem(), selector, deferFuncName, args); err != nil {
		return "", err
	}

	// A *liquidDefer forces a hydro session (and its SSE stream) onto an
	// otherwise-static page: the completion patch has nowhere to go without
	// one.
	sessionID, err := sc.ensure()
	if err != nil {
		return "", fmt.Errorf("deferring %s: establishing session: %w", selector, err)
	}
	token, err := sc.a.newToken()
	if err != nil {
		return "", fmt.Errorf("deferring %s: %w", selector, err)
	}
	// One token, three roles: the fallback slot's data-hydro-id, the child's
	// HydroID (so the loaded child is an ordinary live instance), and its
	// registry key.
	inst.Elem().Field(reg.hydroField).SetString(token)
	if reg.csrfField >= 0 {
		inst.Elem().Field(reg.csrfField).SetString(
			mintCSRF(sc.a.csrfSecret, sessionID, sc.a.limits.SessionIdleTimeout, sc.a.now()))
	}

	sc.a.registerDefer(sessionID, token, inst, reg, sc.reqCtx)
	return token, nil
}

// registerDefer reserves a deferred child's registry entry and starts its
// background load. The entry is put immediately — so it counts against the
// session's component cap (bounded, D20) and holds the token — but starts
// not-ready: no event dispatches against it until the load publishes. The
// load goroutine is owned by the entry, dying with its eviction or the
// session's expiry like a subscription pump (#10).
func (a *App) registerDefer(sessionID, token string, inst reflect.Value, reg *registration, reqCtx Ctx) {
	st := &hydroState{inst: inst, reg: reg} // ready is false until the load publishes
	sess := a.hydro.put(sessionID, token, st, a.now(), a.limits)
	stop, prime := a.startDeferLoad(sess, st, token, sessionID, reqCtx)
	if !a.hydro.attachPump(sessionID, token, stop, prime) {
		// The entry was evicted in the window since put; no eviction path will
		// ever run this stop, so it runs here (mirrors registerHydro).
		stop()
	}
}

// startDeferLoad launches the goroutine that runs a deferred child's load and
// pushes its content, returning the entry's stop (cancels the load and ends
// the goroutine) and prime. The load runs on a context detached from the
// request: the request's own context is cancelled the instant the shell
// ships, which is exactly when the deferred work begins.
//
// Two signals feed the goroutine, and which one fires decides the frame kind.
// prime — the completion and every connect-time re-push (#10) — sends a swap,
// which replaces the fallback slot element wholesale so the child's own root
// lands in the DOM. A subscription emission sends a patch, an inner-HTML
// re-render of the now-present boundary that preserves focus (D21). Routing
// them separately is why a stream that connects after completion still gets a
// swap, not a patch it has no boundary for.
func (a *App) startDeferLoad(sess *hydroSession, st *hydroState, token, sessionID string, reqCtx Ctx) (stop, prime func()) {
	loadCtx, cancel := context.WithCancel(context.Background())
	swapCh := make(chan struct{}, 1)
	dirty := make(chan struct{}, 1)
	stopCh := make(chan struct{})

	prime = coalescingSignal(swapCh)
	d := deferLoad{
		sess: sess, st: st, token: token, sessionID: sessionID, reqCtx: reqCtx,
		swapCh: swapCh, dirty: dirty,
		prime: prime, notify: coalescingSignal(dirty), stopCh: stopCh,
	}
	go a.runDefer(loadCtx, d)

	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			close(stopCh)
		})
	}
	return stop, prime
}

// deferLoad bundles the running state of one deferred load's goroutine. It
// deliberately holds no context — the load context is passed to runDefer as a
// first-class argument, never stored.
type deferLoad struct {
	sess      *hydroSession
	st        *hydroState
	token     string
	sessionID string
	reqCtx    Ctx
	swapCh    <-chan struct{} // a nudge to push the completion as a swap (prime)
	dirty     <-chan struct{} // a nudge to push a subscription re-render as a patch
	prime     func()          // the completion/connect-time swap signal
	notify    func()          // the subscription re-render signal
	stopCh    <-chan struct{} // closed when the entry is torn down
}

// coalescingSignal returns a non-blocking nudge on ch: repeated calls before
// the goroutine drains coalesce into one, never blocking the caller.
func coalescingSignal(ch chan struct{}) func() {
	return func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// runDefer is the deferred entry's goroutine: run the load, then publish the
// child and push it, then serve connect-time re-pushes (swap) and
// subscription re-renders (patch) until the entry is torn down. A panic in a
// deferred render is contained here — there is no request handler above it to
// recover to.
func (a *App) runDefer(loadCtx context.Context, d deferLoad) {
	defer func() {
		if r := recover(); r != nil {
			a.logger.Error("panic in deferred render", "hydroIdPrefix", tokenPrefix(d.token), "panic", r)
		}
	}()

	loadErr := a.loadDeferred(loadCtx, d.st, d.sessionID, d.reqCtx)

	// Torn down mid-load (entry evicted, session expired): the instance is
	// gone from the registry and its patch is dropped.
	select {
	case <-d.stopCh:
		return
	default:
	}
	if loadErr != nil {
		a.logger.Error("deferred load failed", "hydroIdPrefix", tokenPrefix(d.token), "error", loadErr)
	}

	// Publish under the dispatch mutex: activate subscriptions and flip ready
	// at the same instant the instance becomes live, so a dispatched handler
	// never observes a half-loaded instance (D20.1). On load failure the
	// instance stays not-ready and observes nothing — the slot shows an error.
	d.sess.dispatch.Lock()
	if loadErr == nil {
		if sp, ok := d.st.inst.Interface().(SubscriptionProvider); ok {
			d.st.subs = sp.Subscriptions()
		}
		d.st.ready = true
	}
	d.sess.dispatch.Unlock()

	cancels := make([]func(), 0, len(d.st.subs))
	if loadErr == nil {
		for _, sub := range d.st.subs {
			cancels = append(cancels, sub.subscribe(d.notify))
		}
	}

	d.prime() // the first push: the completion swap, or the error slot

	for {
		select {
		case <-d.stopCh:
			for _, cancel := range cancels {
				cancel()
			}
			return
		case <-d.swapCh:
			a.emitDefer(d.sess, d.st, d.token, loadErr, true)
		case <-d.dirty:
			a.emitDefer(d.sess, d.st, d.token, loadErr, false)
		}
	}
}

// loadDeferred runs the deferred child's OnInit — its slow data load — on the
// detached context, or nothing when the component has no initializer (it then
// renders immediately in the background). The Ctx carries the render's params
// and session; the request is cloned onto the load context so accessors work
// without racing the finished request.
func (a *App) loadDeferred(loadCtx context.Context, st *hydroState, sessionID string, reqCtx Ctx) (err error) {
	init, ok := st.inst.Interface().(Initializer)
	if !ok {
		return nil
	}
	// A panicking OnInit becomes a load error — the slot shows the error path
	// rather than taking the whole process down from a detached goroutine.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("deferred OnInit panicked: %v", r)
		}
	}()
	c := Ctx{Context: loadCtx, params: reqCtx.params, session: sessionID}
	if reqCtx.req != nil {
		c.req = reqCtx.req.Clone(loadCtx)
	}
	if e := init.OnInit(c); e != nil {
		return fmt.Errorf("deferred OnInit: %w", e)
	}
	return nil
}

// emitDefer renders one deferred frame and fans it out to the session's open
// streams. asSwap selects the frame kind: a swap replaces the fallback slot
// element with the child's root (the completion and connect-time re-pushes),
// while a patch re-renders the boundary's contents (subscription updates). A
// load error renders the generic error slot as a patch, leaving the fallback
// div as the boundary.
func (a *App) emitDefer(sess *hydroSession, st *hydroState, token string, loadErr error, asSwap bool) {
	var frame sseFrame
	var err error
	switch {
	case loadErr != nil:
		frame, err = patchFrame(sseMsg{HydroID: token, Patch: deferErrorSlot(token)})
	default:
		sess.dispatch.Lock()
		content, rerr := a.renderStateLocked(st, sess.id)
		sess.dispatch.Unlock()
		if rerr != nil {
			a.logger.Error("rendering deferred component", "hydroIdPrefix", tokenPrefix(token), "error", rerr)
			return
		}
		if asSwap {
			frame, err = swapFrame(sseMsg{HydroID: token, Patch: content})
		} else {
			frame, err = patchFrame(sseMsg{HydroID: token, Patch: content})
		}
	}
	if err != nil {
		a.logger.Error("encoding deferred frame", "hydroIdPrefix", tokenPrefix(token), "error", err)
		return
	}
	for _, stream := range a.hydro.sessionStreams(sess) {
		stream.send(frame)
	}
}

// deferErrorSlot is the patch a failed deferred load pushes into its slot: a
// generic message that keeps the slot's data-hydro-id boundary. The failure
// detail stays server-side (logged), never shipped to the client.
func deferErrorSlot(token string) string {
	return `<div data-hydro-id="` + html.EscapeString(token) + `"><p>This section could not be loaded.</p></div>`
}
