package liquid

import (
	"context"
	"sync"
	"time"
)

// Observable is the read-and-subscribe surface shared by BehaviorSubject and
// the derived combinators (Map, CombineLatest, Interval, Throttle). Observe
// and every combinator take an Observable, so a derived value composes over
// either a plain subject or another derived exactly alike, with no wire-format
// or transport change (D25).
type Observable[T any] interface {
	// Value returns the current value synchronously.
	Value() T
	// Subscribe registers fn for future emissions and returns its cancel. The
	// current value is not replayed — read it with Value.
	Subscribe(fn func(T)) (cancel func())
}

// activator is the unexported half of a derived value: the wiring that
// subscribes to upstreams (or runs a poll goroutine) and must be torn down
// when the owning session's registry entry is reaped (D20). A plain
// BehaviorSubject is app-lifetime and does not implement it — its subscribers
// are managed one at a time. Only Derived does, so Observe and the combinators
// start the whole derived chain when it is first observed and stop it when the
// last observer is released.
type activator interface {
	activate() (release func())
}

// Derived is a value computed from one or more upstream Observables. It reads
// and subscribes exactly like a BehaviorSubject — it is backed by one — so it
// composes with Observe, server push (D3), patch swaps (D14), and further
// combinators with no wire-format change (D25). It is inert until observed:
// the framework activates its internal subscriptions through the same
// Subscription lifecycle that owns a component's bindings, reference-counted so
// several observers share one set of upstream subscriptions and the last
// release tears them down. That is the point of D25 — the leak-prone
// subscription lifecycle lives behind the framework boundary, never in
// generated tile code.
type Derived[T any] struct {
	out *BehaviorSubject[T]
	// start wires the upstreams to recompute into out and returns the
	// teardown. Run on the first activation; its result runs on the last
	// release.
	start func() (stop func())

	mu   sync.Mutex
	refs int
	stop func()
}

// Value returns the derived value's current computed value.
func (d *Derived[T]) Value() T { return d.out.Value() }

// Subscribe follows the derived value's emissions, exactly as for a subject.
// Subscribing does not itself start the upstream wiring — activate does, driven
// by Observe — so lifecycle stays owned by the framework, not by whoever reads
// the value.
func (d *Derived[T]) Subscribe(fn func(T)) (cancel func()) { return d.out.Subscribe(fn) }

// activate starts the upstream wiring on the first observer and returns a
// release that stops it once the last observer is gone. Reference-counted, so a
// value observed by several components — or feeding several derived values —
// keeps exactly one live set of upstream subscriptions. release is idempotent.
func (d *Derived[T]) activate() (release func()) {
	d.mu.Lock()
	d.refs++
	if d.refs == 1 {
		d.stop = d.start()
	}
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			d.refs--
			last := d.refs == 0
			stop := d.stop
			if last {
				d.stop = nil
			}
			d.mu.Unlock()
			if last && stop != nil {
				stop()
			}
		})
	}
}

// activateUpstream starts src's wiring if src is itself a derived value, so a
// composed chain (a Throttle over a Map, say) activates end to end. A plain
// subject has no wiring to start, so its release is a no-op.
func activateUpstream[T any](src Observable[T]) (release func()) {
	if a, ok := src.(activator); ok {
		return a.activate()
	}
	return func() {}
}

// Map is a 1→1 projection: every value of src becomes fn(value) in the derived
// value. The subscription to src is framework-owned — reaped when the observing
// session goes (D25) — so generated tiles never manage it.
func Map[T, U any](src Observable[T], fn func(T) U) *Derived[U] {
	d := &Derived[U]{out: NewBehaviorSubject(fn(src.Value()))}
	d.start = func() func() {
		releaseSrc := activateUpstream(src)
		// Recompute from the current value now, in case src changed between
		// construction and this first activation.
		d.out.Next(fn(src.Value()))
		cancel := src.Subscribe(func(v T) { d.out.Next(fn(v)) })
		return func() {
			cancel()
			releaseSrc()
		}
	}
	return d
}

// Interval exposes a periodic poll as a stream: fn runs once now to seed the
// value, then again on every period tick, each result emitted to observers.
// The poll goroutine is tied to the observing session's lifetime exactly like a
// subscription pump — it does not run until the source is observed, and it
// stops on the last release or when ctx is cancelled, whichever comes first
// (D25). There is no unbounded background work: a source nobody observes never
// starts a goroutine, and one that is observed is reaped with its session.
func Interval[T any](ctx context.Context, period time.Duration, fn func() T) *Derived[T] {
	d := &Derived[T]{out: NewBehaviorSubject(fn())}
	d.start = func() func() {
		pollCtx, cancel := context.WithCancel(ctx)
		go func() {
			ticker := time.NewTicker(period)
			defer ticker.Stop()
			for {
				select {
				case <-pollCtx.Done():
					return
				case <-ticker.C:
					d.out.Next(fn())
				}
			}
		}()
		return cancel
	}
	return d
}

// Throttle is backpressure for a chatty source: it samples src at most once
// per window, so a burst of emissions collapses to a single downstream one
// carrying the latest value — not every tick becomes an SSE patch (D25). It
// samples rather than debounces, so a source that never goes quiet still
// advances once per window. The upstream subscription and the sampling
// goroutine are framework-owned and reaped with the observing session.
func Throttle[T any](src Observable[T], window time.Duration) *Derived[T] {
	d := &Derived[T]{out: NewBehaviorSubject(src.Value())}
	d.start = func() func() {
		releaseSrc := activateUpstream(src)

		var mu sync.Mutex
		var latest T
		dirty := false
		cancelSrc := src.Subscribe(func(v T) {
			mu.Lock()
			latest, dirty = v, true
			mu.Unlock()
		})
		// Seed the sampler with current state — after attaching the subscription
		// so no concurrent emission is missed — so the first tick converges the
		// tile even when src changed between construction and this activation.
		mu.Lock()
		latest, dirty = src.Value(), true
		mu.Unlock()

		stop := make(chan struct{})
		go func() {
			ticker := time.NewTicker(window)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					mu.Lock()
					v, send := latest, dirty
					dirty = false
					mu.Unlock()
					if send {
						d.out.Next(v)
					}
				}
			}
		}()

		return func() {
			close(stop)
			cancelSrc()
			releaseSrc()
		}
	}
	return d
}

// CombineLatest recomputes fn from the latest value of both inputs whenever
// *either* one changes. It is the load-bearing combinator of D25: one filter
// control (a date range, an environment selector) fans out to every dependent
// tile without the author wiring N→M subscriptions by hand. Each recompute
// reads a.Value()/b.Value() rather than the emitted value, so it always
// combines current state regardless of which input fired. Both subscriptions
// are framework-owned and reaped together with the observing session.
func CombineLatest[A, B, C any](a Observable[A], b Observable[B], fn func(A, B) C) *Derived[C] {
	d := &Derived[C]{out: NewBehaviorSubject(fn(a.Value(), b.Value()))}
	d.start = func() func() {
		releaseA := activateUpstream(a)
		releaseB := activateUpstream(b)
		recompute := func() { d.out.Next(fn(a.Value(), b.Value())) }
		recompute()
		cancelA := a.Subscribe(func(A) { recompute() })
		cancelB := b.Subscribe(func(B) { recompute() })
		return func() {
			cancelA()
			cancelB()
			releaseA()
			releaseB()
		}
	}
	return d
}
