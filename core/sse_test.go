package liquid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests are white-box for the same reasons hardening_test.go's are:
// idle expiry needs the tickClock, and "the subscription is gone" /
// "the pump goroutine exited" are exactly the invisible-at-the-seam facts
// the leak guarantees are about (D20). Wire-visible push behavior lives in
// liquidtest/sse_test.go.

// pushMetric is the minimal subscribing component.
type pushMetric struct {
	HydroID string
	Feed    *BehaviorSubject[int] `inject:""`
	Reading int
}

func (m *pushMetric) Selector() string { return "app-push-metric" }

func (m *pushMetric) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{ .Reading }}</div>`
}

// OnInit seeds the render from the subject's current value.
func (m *pushMetric) OnInit(Ctx) error {
	m.Reading = m.Feed.Value()
	return nil
}

// Subscriptions declares the live binding.
func (m *pushMetric) Subscriptions() []Subscription {
	return []Subscription{Observe(m.Feed, func(v int) { m.Reading = v })}
}

// newPushApp assembles an app serving pushMetric over feed.
func newPushApp(t *testing.T, feed *BehaviorSubject[int], opts ...Option) *App {
	t.Helper()
	app := New(opts...)
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &pushMetric{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return app
}

// renderWBAs GETs / carrying an existing session cookie, for registering
// more instances under one session.
func renderWBAs(t *testing.T, app *App, sessionID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("render status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// waitForGoroutines polls until the goroutine count settles at or below
// want; pumps exit asynchronously after their stop, so leak checks converge
// rather than assert instantly.
func waitForGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine count stuck at %d, want <= %d — a subscription pump leaked (D20)", runtime.NumGoroutine(), want)
}

func TestRequestScopedValueReadsLeaveNoSubscription(t *testing.T) {
	feed := NewBehaviorSubject(9)
	app := New()
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	// valueReader is defined below: not interactive, no Subscriptions — it
	// only reads Value in OnInit.
	if err := app.Route("/", &valueReader{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ">9<") {
		t.Fatalf("render = %d %q, want 200 showing the current value 9", rec.Code, rec.Body.String())
	}
	if n := feed.subscriberCount(); n != 0 {
		t.Errorf("subscriberCount() after a request-scoped read = %d, want 0", n)
	}
}

// valueReader reads the subject's current value at render and never
// subscribes.
type valueReader struct {
	Feed    *BehaviorSubject[int] `inject:""`
	Reading int
}

func (r *valueReader) Selector() string { return "app-value-reader" }

func (r *valueReader) Template() string { return `<p>{{ .Reading }}</p>` }

// OnInit is the request-scoped read.
func (r *valueReader) OnInit(Ctx) error {
	r.Reading = r.Feed.Value()
	return nil
}

func TestIdleExpiryCancelsSubscriptionsAndReapsPumps(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_700_000_000, 0)}
	feed := NewBehaviorSubject(0)
	app := newPushApp(t, feed, WithLimits(Limits{SessionIdleTimeout: time.Minute}))
	app.now = clock.now
	baseline := runtime.NumGoroutine()

	renderWB(t, app)
	if n := feed.subscriberCount(); n != 1 {
		t.Fatalf("subscriberCount() after an interactive render = %d, want 1", n)
	}

	// Idle past the window; the next registration sweeps the session out,
	// which must cancel its subscription and stop its pump (D20).
	clock.t = clock.t.Add(2 * time.Minute)
	renderWB(t, app)

	if n := feed.subscriberCount(); n != 1 {
		t.Errorf("subscriberCount() after the sweep = %d, want 1 (the fresh session only)", n)
	}
	// The expired session's pump must exit; the fresh session still runs
	// one.
	waitForGoroutines(t, baseline+1)
}

func TestSessionEvictionCancelsSubscriptions(t *testing.T) {
	feed := NewBehaviorSubject(0)
	app := newPushApp(t, feed, WithLimits(Limits{MaxSessions: 1}))
	baseline := runtime.NumGoroutine()

	renderWB(t, app)
	// A second browser session at the cap evicts the first (LRU), taking its
	// subscription — and its pump goroutine — with it.
	renderWB(t, app)

	if n := feed.subscriberCount(); n != 1 {
		t.Errorf("subscriberCount() after LRU session eviction = %d, want 1", n)
	}
	waitForGoroutines(t, baseline+1)
}

func TestEntryEvictionCancelsItsSubscription(t *testing.T) {
	feed := NewBehaviorSubject(0)
	app := newPushApp(t, feed, WithLimits(Limits{MaxComponentsPerSession: 1}))

	baseline := runtime.NumGoroutine()
	first := renderWB(t, app)
	// A second instance under the same session breaches the per-session cap
	// and evicts the first entry — and its subscription and pump.
	renderWBAs(t, app, first.id)

	if n := feed.subscriberCount(); n != 1 {
		t.Errorf("subscriberCount() after per-session entry eviction = %d, want 1", n)
	}
	waitForGoroutines(t, baseline+1)
}

func TestRouteRejectsASubscriberThatIsNotInteractive(t *testing.T) {
	feed := NewBehaviorSubject(0)
	app := New()
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}

	// A component declaring Subscriptions but no HydroID field has no patch
	// boundary to push to; that is a compile-shape bug and must fail loudly
	// at registration, not silently never push.
	err := app.Route("/", &boundlessSubscriber{})
	if err == nil {
		t.Fatal("Route accepted a SubscriptionProvider with no HydroID field; want a registration error")
	}
	if !strings.Contains(err.Error(), "HydroID") {
		t.Errorf("registration error %q does not name the missing HydroID field", err)
	}
}

// boundlessSubscriber declares subscriptions without being interactive — a
// contract violation Route must refuse.
type boundlessSubscriber struct {
	Feed    *BehaviorSubject[int] `inject:""`
	Reading int
}

func (b *boundlessSubscriber) Selector() string { return "app-boundless" }

func (b *boundlessSubscriber) Template() string { return `<p>{{ .Reading }}</p>` }

// Subscriptions declares a binding the component has no patch boundary for.
func (b *boundlessSubscriber) Subscriptions() []Subscription {
	return []Subscription{Observe(b.Feed, func(v int) { b.Reading = v })}
}

// readPush blocks for one frame the pump fans onto a white-box stream and
// decodes it, failing the test if none arrives. It reads stream.ch directly —
// the pump's output — rather than driving the serveHydroSSE wire path, which
// liquidtest already covers.
func readPush(t *testing.T, stream *sseStream) sseMsg {
	t.Helper()
	select {
	case f := <-stream.ch:
		var msg sseMsg
		if err := json.Unmarshal([]byte(f.data), &msg); err != nil {
			t.Fatalf("decoding pushed frame %q: %v", f.data, err)
		}
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a pushed frame")
		return sseMsg{}
	}
}

// White-box like the #46 event-path proof (hardening_test.go): the sliding
// window needs the tickClock, and a pump kept alive only by push is exactly
// the invisible-at-the-wire fact this is about. It is the SSE analog of
// TestEventPatchReMintsCSRFTokenTrackingTheSlidingWindow.
func TestPushedPatchReMintsCSRFTrackingTheSlidingWindow(t *testing.T) {
	idle := time.Hour
	clock := &tickClock{t: time.Unix(1_700_000_000, 0)}
	feed := NewBehaviorSubject(0)
	app := newPushApp(t, feed, WithLimits(Limits{SessionIdleTimeout: idle}))
	app.now = clock.now

	sess := renderWB(t, app)
	original := sess.csrf

	// Attach a stream as the runtime's EventSource does. The connect-time
	// prime renders at the current clock — still the render instant, since we
	// have not advanced it — so draining it here is deterministic and leaves
	// the buffer empty for the emission below.
	stream := newSSEStream()
	if !app.hydro.attachStream(sess.id, stream, app.now(), idle, app.limits.MaxStreamsPerSession) {
		t.Fatal("attachStream on a live session returned false")
	}
	defer stream.close()
	if connect := readPush(t, stream); connect.CSRF == "" {
		t.Fatal("connect-time push carried no csrf token (#52)")
	}

	// The page is now kept alive only by server push. Time slides toward the
	// original token's horizon; the pushed patch must carry a token minted
	// against the current clock, not the render's fixed expiry.
	clock.t = clock.t.Add(40 * time.Minute)
	feed.Next(1)

	msg := readPush(t, stream)
	if msg.CSRF == "" {
		t.Fatal("pushed patch carried no re-minted csrf token (#52)")
	}
	if msg.CSRF == original {
		t.Error("pushed patch re-served the original token; it must re-mint against the current clock")
	}
	expiry, err := strconv.ParseInt(strings.SplitN(msg.CSRF, ":", 2)[0], 10, 64)
	if err != nil {
		t.Fatalf("pushed token %q is not expiry:signature: %v", msg.CSRF, err)
	}
	if want := clock.t.Add(idle).Unix(); expiry != want {
		t.Errorf("pushed token expiry = %d, want %d (now + idle window)", expiry, want)
	}

	// The point of #52: a push-only page stays usable past the original
	// horizon. Advance beyond where the original expired — the pushed token
	// still validates for the session while the original now does not, so the
	// next user event dispatches instead of wedging on a 403.
	clock.t = clock.t.Add(50 * time.Minute) // 90 min since render; original expired at 60
	if validCSRF(app.csrfSecret, original, sess.id, app.now()) {
		t.Fatal("test premise broken: the original token has not expired at 90 minutes")
	}
	if !validCSRF(app.csrfSecret, msg.CSRF, sess.id, app.now()) {
		t.Error("the pushed re-minted token does not validate past the original horizon; a push-only page would still wedge (#52)")
	}
}

func TestSlowStreamIsDisconnectedRatherThanBlocked(t *testing.T) {
	st := newSSEStream()
	// A reader that never drains: the buffer fills, and the next send must
	// close the stream instead of blocking the pump or dropping into
	// silently stale delivery (D20).
	for range sseBufferSize {
		st.send(sseFrame{event: "patch", data: `{"hydroId":"x","patch":"<div></div>"}`})
	}
	select {
	case <-st.done:
		t.Fatal("stream closed before its buffer filled")
	default:
	}

	st.send(sseFrame{event: "patch", data: `{"hydroId":"x","patch":"<div></div>"}`})

	select {
	case <-st.done:
	default:
		t.Fatal("overflowing send left the stream open; want it closed so the client reconnects (D20)")
	}
}
