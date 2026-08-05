package liquid

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// Combinators are white-box tested here for the same reason BehaviorSubject
// is (subject_test.go): a derived value's *internal* subscription to its
// upstream — the thing that must be reaped on session GC or it grows an
// app-lifetime subject's subscriber set without bound (D25, the CLAUDE.md
// bounded-registry invariant) — is invisible at the HTTP seam. subscriberCount
// on the upstream is the leak-check hook. Wire-visible push behavior lives in
// liquidtest/combinators_test.go; reaping across the real eviction/expiry
// paths lives in sse_test.go's registry tests.

func TestMapProjectsCurrentValueBeforeActivation(t *testing.T) {
	src := NewBehaviorSubject(2)
	doubled := Map(src, func(v int) int { return v * 2 })

	if got := doubled.Value(); got != 4 {
		t.Fatalf("Value() before activation = %d, want 4 (projection of the current source)", got)
	}
	if n := src.subscriberCount(); n != 0 {
		t.Fatalf("an un-observed Map holds %d source subscriptions, want 0 — a derived value is inert until observed", n)
	}
}

func TestMapRecomputesOnSourceEmissionWhileObserved(t *testing.T) {
	src := NewBehaviorSubject(2)
	doubled := Map(src, func(v int) int { return v * 2 })

	// Activate through the same Subscription seam the pump drives (Observe →
	// subscribe(notify)); notify stands in for the pump's dirty signal.
	notified := 0
	sub := Observe(doubled, func(int) {})
	cancel := sub.subscribe(func() { notified++ })
	defer cancel()

	if n := src.subscriberCount(); n != 1 {
		t.Fatalf("observed Map holds %d source subscriptions, want 1", n)
	}

	src.Next(5)
	if got := doubled.Value(); got != 10 {
		t.Fatalf("Value() after source Next(5) = %d, want 10 (recomputed projection)", got)
	}
	if notified == 0 {
		t.Fatal("observer was never notified of the derived emission")
	}
}

func TestMapReapsItsSourceSubscriptionOnCancel(t *testing.T) {
	src := NewBehaviorSubject(2)
	doubled := Map(src, func(v int) int { return v * 2 })

	sub := Observe(doubled, func(int) {})
	cancel := sub.subscribe(func() {})
	if n := src.subscriberCount(); n != 1 {
		t.Fatalf("subscriberCount() while observed = %d, want 1", n)
	}

	cancel()
	if n := src.subscriberCount(); n != 0 {
		t.Fatalf("subscriberCount() after cancel = %d, want 0 — the framework must reap the derived's internal subscription (D25)", n)
	}

	// A torn-down derived stops recomputing: it holds its last computed value
	// (4, the projection of 2) and ignores further source emissions.
	src.Next(100)
	if got := doubled.Value(); got != 4 {
		t.Fatalf("Value() after cancel = %d, want 4 (no recompute once reaped)", got)
	}
}

func TestCombineLatestRecomputesOnEitherInput(t *testing.T) {
	a := NewBehaviorSubject(3)
	b := NewBehaviorSubject(4)
	sum := CombineLatest(a, b, func(x, y int) int { return x + y })

	if got := sum.Value(); got != 7 {
		t.Fatalf("Value() before activation = %d, want 7 (3+4 of current inputs)", got)
	}

	sub := Observe(sum, func(int) {})
	cancel := sub.subscribe(func() {})
	defer cancel()

	// A change to the FIRST input recomputes.
	a.Next(10)
	if got := sum.Value(); got != 14 {
		t.Fatalf("Value() after a.Next(10) = %d, want 14 (10+4)", got)
	}
	// A change to the SECOND input recomputes too — this is what lets one
	// filter control fan out to every dependent tile (D25).
	b.Next(5)
	if got := sum.Value(); got != 15 {
		t.Fatalf("Value() after b.Next(5) = %d, want 15 (10+5)", got)
	}
}

func TestCombineLatestReapsBothSourcesOnCancel(t *testing.T) {
	a := NewBehaviorSubject(1)
	b := NewBehaviorSubject(2)
	sum := CombineLatest(a, b, func(x, y int) int { return x + y })

	if a.subscriberCount() != 0 || b.subscriberCount() != 0 {
		t.Fatalf("un-observed CombineLatest already holds source subscriptions (a=%d, b=%d), want 0/0", a.subscriberCount(), b.subscriberCount())
	}

	sub := Observe(sum, func(int) {})
	cancel := sub.subscribe(func() {})
	if a.subscriberCount() != 1 || b.subscriberCount() != 1 {
		t.Fatalf("observed CombineLatest holds a=%d, b=%d source subscriptions, want 1/1", a.subscriberCount(), b.subscriberCount())
	}

	cancel()
	if a.subscriberCount() != 0 || b.subscriberCount() != 0 {
		t.Fatalf("subscriberCount() after cancel = a:%d b:%d, want 0/0 — both internal subscriptions must be reaped (D25)", a.subscriberCount(), b.subscriberCount())
	}
}

func TestCombinatorsComposeAndReapTheWholeChain(t *testing.T) {
	src := NewBehaviorSubject(2)
	// A derived value over another derived value: Map(Map(...)). Observing only
	// the outer one must activate — and later reap — the entire chain, since
	// combinators are meant to compose (D25).
	inner := Map(src, func(v int) int { return v * 10 })
	outer := Map(inner, func(v int) int { return v + 1 })

	if got := outer.Value(); got != 21 {
		t.Fatalf("composed Value() before activation = %d, want 21 ((2*10)+1)", got)
	}
	if n := src.subscriberCount(); n != 0 {
		t.Fatalf("an un-observed chain already holds %d source subscriptions, want 0", n)
	}

	sub := Observe(outer, func(int) {})
	cancel := sub.subscribe(func() {})
	if n := src.subscriberCount(); n != 1 {
		t.Fatalf("observing the outer derived left %d source subscriptions, want 1 — activation must reach through the chain", n)
	}

	src.Next(5)
	if got := outer.Value(); got != 51 {
		t.Fatalf("composed Value() after src.Next(5) = %d, want 51 ((5*10)+1)", got)
	}

	cancel()
	if n := src.subscriberCount(); n != 0 {
		t.Fatalf("subscriberCount() after cancel = %d, want 0 — releasing the outer must reap the whole chain (D25)", n)
	}
}

func TestIntervalEmitsFnResultOnEveryTickWhileActive(t *testing.T) {
	var calls int64
	src := Interval(context.Background(), 2*time.Millisecond, func() int64 {
		return atomic.AddInt64(&calls, 1)
	})

	got := make(chan int64, 64)
	unsub := src.Subscribe(func(v int64) {
		select {
		case got <- v:
		default:
		}
	})
	defer unsub()

	// Activate through the same seam the pump drives; only then does the poll
	// goroutine run — no unbounded background work before observation (D25).
	sub := Observe(src, func(int64) {})
	release := sub.subscribe(func() {})
	defer release()

	// Three distinct emissions prove the source ticks repeatedly.
	var prev int64
	for i := 0; i < 3; i++ {
		select {
		case v := <-got:
			if v <= prev {
				t.Fatalf("emission %d = %d, want a fresh (increasing) poll result", i, v)
			}
			prev = v
		case <-time.After(2 * time.Second):
			t.Fatalf("Interval stopped emitting after %d ticks", i)
		}
	}
}

func TestIntervalStopsPollingWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int64
	src := Interval(ctx, 2*time.Millisecond, func() int64 {
		return atomic.AddInt64(&calls, 1)
	})

	sub := Observe(src, func(int64) {})
	release := sub.subscribe(func() {})
	defer release()

	time.Sleep(20 * time.Millisecond) // let it tick several times
	cancel()
	time.Sleep(10 * time.Millisecond) // let the in-flight tick settle
	stopped := atomic.LoadInt64(&calls)

	time.Sleep(40 * time.Millisecond) // ~20 more ticks would land if still running
	if got := atomic.LoadInt64(&calls); got != stopped {
		t.Fatalf("Interval called fn %d more times after ctx cancel (%d → %d); want it cancelled by its context", got-stopped, stopped, got)
	}
}

func TestIntervalReapsItsPollGoroutineOnRelease(t *testing.T) {
	baseline := runtime.NumGoroutine()
	src := Interval(context.Background(), 2*time.Millisecond, func() int { return 0 })

	sub := Observe(src, func(int) {})
	release := sub.subscribe(func() {})
	// The poll goroutine is now running above the baseline.

	release()
	// Releasing the last observer must stop the poll goroutine (D25): no
	// unbounded background work outlives the observing session.
	waitForGoroutines(t, baseline)
}

func TestThrottleCoalescesABurstToAtMostOnePerWindowAndConverges(t *testing.T) {
	const window = 25 * time.Millisecond
	src := NewBehaviorSubject(-1)
	throttled := Throttle(src, window)

	var emissions int64
	unsub := throttled.Subscribe(func(int) { atomic.AddInt64(&emissions, 1) })
	defer unsub()

	sub := Observe(throttled, func(int) {})
	release := sub.subscribe(func() {})
	defer release()

	// A tight burst well inside one window: backpressure must collapse it, so
	// not every tick becomes an SSE patch (D25).
	for i := 0; i < 100; i++ {
		src.Next(i)
	}

	// Wait a few windows for the sampler to fire and settle.
	time.Sleep(4 * window)

	got := atomic.LoadInt64(&emissions)
	if got < 1 {
		t.Fatalf("throttled output never emitted for a 100-value burst; want the latest coalesced through")
	}
	if got > 3 {
		t.Fatalf("throttled output emitted %d times for a burst inside a few windows; want it coalesced to at most one per window", got)
	}
	if v := throttled.Value(); v != 99 {
		t.Fatalf("throttled Value() = %d after the burst, want 99 (converges on the latest source value)", v)
	}
}

func TestThrottleConvergesToCurrentSourceAfterActivation(t *testing.T) {
	src := NewBehaviorSubject(1)
	throttled := Throttle(src, 10*time.Millisecond)

	// The source changes before the tile is ever observed (activated), then
	// goes quiet. A sampler that only watches emissions from activation onward
	// would miss this and stay stuck on the stale construction seed; the
	// throttled value must still converge to current source (D25).
	src.Next(7)

	sub := Observe(throttled, func(int) {})
	release := sub.subscribe(func() {})
	defer release()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if throttled.Value() == 7 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("throttled Value() = %d after activation, want 7 (converges to current source, not the construction seed)", throttled.Value())
}

func TestThrottleReapsUpstreamAndGoroutineOnRelease(t *testing.T) {
	baseline := runtime.NumGoroutine()
	src := NewBehaviorSubject(0)
	throttled := Throttle(src, 5*time.Millisecond)

	sub := Observe(throttled, func(int) {})
	release := sub.subscribe(func() {})
	if n := src.subscriberCount(); n != 1 {
		t.Fatalf("observed Throttle holds %d source subscriptions, want 1", n)
	}

	release()
	if n := src.subscriberCount(); n != 0 {
		t.Fatalf("subscriberCount() after release = %d, want 0 — the upstream subscription must be reaped (D25)", n)
	}
	waitForGoroutines(t, baseline)
}

// The registry-path reaping tests below prove the load-bearing D25 guarantee
// end to end: a component observes a *derived* value (here a Throttle over an
// app-lifetime injected subject), so the ONLY thing subscribing to that
// upstream subject is the combinator's framework-owned internal wiring, and
// the only extra goroutines are the instance's pump plus the Throttle sampler.
// When the registry entry is reaped on any path, the upstream's subscriber
// count must fall back to the surviving sessions' and both goroutines must
// exit — exactly the leak sse_test.go proves for a bare subscription, now for
// derived state. These mirror sse_test.go's three reaping tests deliberately.

// derivedTile observes a Throttle over an injected app-lifetime subject. Each
// live instance therefore holds one Orders subscription (the sampler's) and
// runs two goroutines (the pump and the Throttle sampler).
type derivedTile struct {
	HydroID  string
	Orders   *BehaviorSubject[int] `inject:""`
	smoothed *Derived[int]
	Total    int
}

func (d *derivedTile) Selector() string { return "app-derived-tile" }

func (d *derivedTile) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{ .Total }}</div>`
}

// OnInit builds the derived value; it runs before Subscriptions() on the same
// instance, so the binding below observes it.
func (d *derivedTile) OnInit(Ctx) error {
	d.smoothed = Throttle(d.Orders, 10*time.Millisecond)
	d.Total = d.smoothed.Value()
	return nil
}

func (d *derivedTile) Subscriptions() []Subscription {
	return []Subscription{Observe(d.smoothed, func(v int) { d.Total = v })}
}

// newDerivedApp assembles an app serving derivedTile over an app-lifetime
// Orders subject.
func newDerivedApp(t *testing.T, orders *BehaviorSubject[int], opts ...Option) *App {
	t.Helper()
	app := New(opts...)
	if err := app.Provide(orders); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &derivedTile{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return app
}

// perTile is the goroutine cost of one live derivedTile: its subscription pump
// plus the Throttle sampler.
const perTile = 2

func TestIdleExpiryReapsDerivedSubscriptionsAndGoroutines(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_700_000_000, 0)}
	orders := NewBehaviorSubject(0)
	app := newDerivedApp(t, orders, WithLimits(Limits{SessionIdleTimeout: time.Minute}))
	app.now = clock.now
	baseline := runtime.NumGoroutine()

	renderWB(t, app)
	if n := orders.subscriberCount(); n != 1 {
		t.Fatalf("after an interactive render the derived value holds %d Orders subscriptions, want 1", n)
	}

	// Idle past the window; the next registration sweeps the session out, which
	// must reap the derived's upstream subscription and stop both goroutines.
	clock.t = clock.t.Add(2 * time.Minute)
	renderWB(t, app)

	if n := orders.subscriberCount(); n != 1 {
		t.Errorf("after the idle sweep Orders has %d subscribers, want 1 (the fresh session's derived only)", n)
	}
	waitForGoroutines(t, baseline+perTile)
}

func TestSessionEvictionReapsDerivedSubscriptionsAndGoroutines(t *testing.T) {
	orders := NewBehaviorSubject(0)
	app := newDerivedApp(t, orders, WithLimits(Limits{MaxSessions: 1}))
	baseline := runtime.NumGoroutine()

	renderWB(t, app)
	// A second browser session at the cap evicts the first (LRU), taking its
	// derived subscription and both goroutines with it.
	renderWB(t, app)

	if n := orders.subscriberCount(); n != 1 {
		t.Errorf("after LRU session eviction Orders has %d subscribers, want 1", n)
	}
	waitForGoroutines(t, baseline+perTile)
}

func TestEntryEvictionReapsDerivedSubscriptionAndGoroutines(t *testing.T) {
	orders := NewBehaviorSubject(0)
	app := newDerivedApp(t, orders, WithLimits(Limits{MaxComponentsPerSession: 1}))
	baseline := runtime.NumGoroutine()

	first := renderWB(t, app)
	// A second instance under the same session breaches the per-session cap and
	// evicts the first entry — and its derived subscription and both goroutines.
	renderWBAs(t, app, first.id)

	if n := orders.subscriberCount(); n != 1 {
		t.Errorf("after per-session entry eviction Orders has %d subscribers, want 1", n)
	}
	waitForGoroutines(t, baseline+perTile)
}
