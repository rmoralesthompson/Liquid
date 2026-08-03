package liquid

import (
	"net/http"
	"net/http/httptest"
	"runtime"
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
