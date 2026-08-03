package liquid

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests are white-box: the deferred load's goroutine lifecycle (starts,
// pushes, dies with its entry) and the ready gate are exactly the
// invisible-at-the-seam facts #26's guarantees are about, like the
// subscription-pump tests in sse_test.go. The parent template embeds the
// {{liquidDefer …}} call the compiler emits, so the runtime is exercised
// without the AOT compiler in the loop.

// deferGate lets a test hold a deferred load open and then release or fail it.
type deferGate struct {
	started chan struct{} // OnInit signals (once) that it has begun
	release chan struct{} // OnInit blocks until this is closed
	failErr error         // when non-nil, OnInit returns it after release
}

func newDeferGate() *deferGate {
	return &deferGate{started: make(chan struct{}, 1), release: make(chan struct{})}
}

// slowStats is the deferred child: its OnInit is the slow load, gated by the
// injected deferGate. It carries a HydroID (so it can be deferred at all), an
// [input]-bound Label, and one allowlisted action to prove it is a live
// instance once loaded.
type slowStats struct {
	HydroID string
	Gate    *deferGate `inject:""`
	Label   string
	Value   int
}

func (s *slowStats) Selector() string { return "app-slow-stats" }

func (s *slowStats) Template() string {
	return `<section data-hydro-id="{{ .HydroID }}" class="stats"><p id="body">{{ .Label }}: {{ .Value }}</p></section>`
}

func (s *slowStats) OnInit(ctx Ctx) error {
	if s.Gate != nil {
		select {
		case s.Gate.started <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("deferred load cancelled: %w", ctx.Err())
		case <-s.Gate.release:
		}
		if s.Gate.failErr != nil {
			return s.Gate.failErr
		}
	}
	s.Value = 42
	return nil
}

func (s *slowStats) Bump() { s.Value++ }

func (s *slowStats) Actions() []string { return []string{"Bump"} }

// deferParent is a non-interactive page that defers slowStats; the defer is
// what forces the page's hydro session.
type deferParent struct {
	Topic string
}

func (p *deferParent) Selector() string { return "app-defer-parent" }

func (p *deferParent) Template() string {
	return `<main><div data-hydro-id="{{liquidDefer "app-slow-stats" "label" .Topic}}"><p>Loading…</p></div></main>`
}

// newDeferApp assembles a page deferring slowStats over the gate.
func newDeferApp(t *testing.T, gate *deferGate, opts ...Option) *App {
	t.Helper()
	app := New(opts...)
	if err := app.Provide(gate); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Register(&slowStats{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &deferParent{Topic: "Signups"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return app
}

// renderDeferWB GETs / and returns the established session; the fallback slot's
// data-hydro-id is the defer token, so wbSession.hydro carries it. It also
// asserts the fallback — not the real content — is what shipped.
func renderDeferWB(t *testing.T, app *App) wbSession {
	t.Helper()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("render status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Loading…") {
		t.Fatalf("shipped page missing the fallback: %q", body)
	}
	if strings.Contains(body, "Signups: 42") {
		t.Fatalf("deferred content rendered inline instead of as a fallback: %q", body)
	}
	var id string
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sessionCookieName {
			id = ck.Value
		}
	}
	hm := wbHydroIDPattern.FindStringSubmatch(body)
	cm := wbCSRFPattern.FindStringSubmatch(body)
	if id == "" || hm == nil || cm == nil {
		t.Fatalf("deferred render missing session, hydro, or csrf token: %q", body)
	}
	return wbSession{id: id, hydro: hm[1], csrf: cm[1]}
}

// attachWBStream opens an SSE stream on the session for reading pushed frames.
func attachWBStream(t *testing.T, app *App, sessionID string) *sseStream {
	t.Helper()
	stream := newSSEStream()
	if !app.hydro.attachStream(sessionID, stream, app.now(), app.limits.SessionIdleTimeout, app.limits.MaxStreamsPerSession) {
		t.Fatal("attachStream: session not live")
	}
	return stream
}

// readFrame reads one pushed frame or fails on timeout, returning the frame's
// event kind and its decoded message.
func readFrame(t *testing.T, stream *sseStream) (string, sseMsg) {
	t.Helper()
	select {
	case f := <-stream.ch:
		var msg sseMsg
		if err := json.Unmarshal([]byte(f.data), &msg); err != nil {
			t.Fatalf("decoding frame data %q: %v", f.data, err)
		}
		return f.event, msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a pushed frame")
		return "", sseMsg{}
	}
}

func TestDeferShipsFallbackThenPushesCompletionSwap(t *testing.T) {
	gate := newDeferGate()
	app := newDeferApp(t, gate)

	sess := renderDeferWB(t, app)
	stream := attachWBStream(t, app, sess.id)

	// Let the load finish; the completion arrives as a swap carrying the
	// child's own root element (so its attributes survive), bound to the slot
	// token — with the [input]-bound label proving binding flowed through the
	// defer.
	close(gate.release)
	event, msg := readFrame(t, stream)
	if event != frameEventSwap {
		t.Errorf("completion frame event = %q, want \"swap\"", event)
	}
	if !strings.Contains(msg.Patch, "<section") || !strings.Contains(msg.Patch, "Signups: 42") {
		t.Errorf("swap frame does not carry the rendered child root: %q", msg.Patch)
	}
	if msg.HydroID != sess.hydro {
		t.Errorf("swap frame addressed to %q, want the slot token %q", msg.HydroID, sess.hydro)
	}
}

func TestDeferLoadErrorPushesErrorSlotNotSwap(t *testing.T) {
	gate := newDeferGate()
	gate.failErr = errors.New("upstream down")
	app := newDeferApp(t, gate)

	sess := renderDeferWB(t, app)
	stream := attachWBStream(t, app, sess.id)

	close(gate.release)
	event, msg := readFrame(t, stream)
	// A failed load keeps the fallback div as the boundary — an ordinary
	// patch of a generic message, never a swap, and never the upstream error
	// detail.
	if event != frameEventPatch {
		t.Errorf("error frame event = %q, want \"patch\"", event)
	}
	if !strings.Contains(msg.Patch, "could not be loaded") {
		t.Errorf("error frame missing the generic message: %q", msg.Patch)
	}
	if strings.Contains(msg.Patch, "upstream down") {
		t.Errorf("error frame leaked the load-failure detail: %q", msg.Patch)
	}
}

func TestDeferEntryNotDispatchableUntilLoaded(t *testing.T) {
	gate := newDeferGate()
	app := newDeferApp(t, gate)

	sess := renderDeferWB(t, app)
	<-gate.started // the load is in flight

	// An event against the slot token before the load publishes must miss: the
	// instance is still being mutated off the dispatch mutex (D20.1).
	if code := fireWB(t, app, sess, "Bump"); code != http.StatusNotFound {
		t.Fatalf("dispatch to a still-loading deferred instance = %d, want 404", code)
	}

	close(gate.release)

	// Once loaded it is an ordinary live instance: its action dispatches.
	deadline := time.Now().Add(2 * time.Second)
	for {
		code := fireWB(t, app, sess, "Bump")
		if code == http.StatusOK {
			break
		}
		if code != http.StatusNotFound {
			t.Fatalf("dispatch after load = %d, want 200", code)
		}
		if time.Now().After(deadline) {
			t.Fatal("deferred instance never became dispatchable after its load completed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDeferLoadIsCancelledAndReapedOnSessionExpiry(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_700_000_000, 0)}
	gate := newDeferGate() // never released: only cancellation can end the load
	baseline := runtime.NumGoroutine()

	app := newDeferApp(t, gate, WithLimits(Limits{SessionIdleTimeout: time.Minute}))
	app.now = clock.now

	sess := renderDeferWB(t, app)
	<-gate.started // the load goroutine is parked in OnInit

	// Idle past the window: expiring the session must cancel the load context
	// and reap its goroutine, dropping the never-completed patch (no leak,
	// D20).
	clock.t = clock.t.Add(2 * time.Minute)
	if app.hydro.touch(sess.id, clock.t, app.limits.SessionIdleTimeout) {
		t.Fatal("session should have expired past its idle window")
	}
	waitForGoroutines(t, baseline)
}

func TestDeferPrimingPushesToAStreamThatConnectsAfterCompletion(t *testing.T) {
	gate := newDeferGate()
	app := newDeferApp(t, gate)

	sess := renderDeferWB(t, app)

	// The load completes during the no-SSE-yet window — before any stream is
	// open. The completion has nowhere to go.
	<-gate.started
	close(gate.release)
	// Give the goroutine time to publish before a stream exists.
	if !waitFor(func() bool { return fireWB(t, app, sess, "Bump") == http.StatusOK }) {
		t.Fatal("deferred load never published")
	}

	// A stream connecting now must be primed with current state (#10), so the
	// swap is not lost to the connect-time gap.
	stream := attachWBStream(t, app, sess.id)
	event, msg := readFrame(t, stream)
	if event != frameEventSwap {
		t.Errorf("primed frame event = %q, want \"swap\" (the deferred completion)", event)
	}
	if !strings.Contains(msg.Patch, "Signups") {
		t.Errorf("primed frame does not carry the deferred content: %q", msg.Patch)
	}
}

// liveMetric is a deferred child that also subscribes: once its load
// completes and it is swapped in, subject emissions push live updates.
type liveMetric struct {
	HydroID string
	Gate    *deferGate            `inject:""`
	Feed    *BehaviorSubject[int] `inject:""`
	Reading int
}

func (m *liveMetric) Selector() string { return "app-live-metric" }

func (m *liveMetric) Template() string {
	return `<section data-hydro-id="{{ .HydroID }}"><span id="reading">{{ .Reading }}</span></section>`
}

func (m *liveMetric) OnInit(ctx Ctx) error {
	if m.Gate != nil {
		select {
		case m.Gate.started <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("deferred load cancelled: %w", ctx.Err())
		case <-m.Gate.release:
		}
	}
	m.Reading = m.Feed.Value()
	return nil
}

func (m *liveMetric) Subscriptions() []Subscription {
	return []Subscription{Observe(m.Feed, func(v int) { m.Reading = v })}
}

type deferLiveParent struct{}

func (p *deferLiveParent) Selector() string { return "app-defer-live-parent" }

func (p *deferLiveParent) Template() string {
	return `<main><div data-hydro-id="{{liquidDefer "app-live-metric"}}"><p>Loading…</p></div></main>`
}

func TestDeferredSubscriberPushesLivePatchesAfterSwap(t *testing.T) {
	gate := newDeferGate()
	feed := NewBehaviorSubject(3)
	app := New()
	if err := app.Provide(gate); err != nil {
		t.Fatalf("Provide gate: %v", err)
	}
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide feed: %v", err)
	}
	if err := app.Register(&liveMetric{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &deferLiveParent{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	sess := renderDeferWB(t, app)
	stream := attachWBStream(t, app, sess.id)

	// Completion arrives as a swap carrying the current feed value.
	close(gate.release)
	if event, msg := readFrame(t, stream); event != frameEventSwap || !strings.Contains(msg.Patch, ">3<") {
		t.Fatalf("completion frame = (%q, %q), want a swap showing the initial reading 3", event, msg.Patch)
	}

	// Now that it is live, a subject emission pushes an ordinary patch (not a
	// swap) at the boundary the swap established.
	feed.Next(9)
	event, msg := readFrame(t, stream)
	if event != frameEventPatch {
		t.Errorf("live update frame event = %q, want \"patch\"", event)
	}
	if !strings.Contains(msg.Patch, ">9<") {
		t.Errorf("live update did not carry the new reading: %q", msg.Patch)
	}
}

// waitFor polls cond up to two seconds.
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
