package liquid

import "sync"

// BehaviorSubject is mutex-guarded observable state (D3): it always holds a
// current value, readable synchronously with Value, and notifies subscribers
// on every Next. App-lifetime subjects belong in services registered with
// Provide; request-scoped reads use Value without subscribing — only
// interactive sessions hold subscriptions, and the framework cancels those
// when the session goes (D20).
type BehaviorSubject[T any] struct {
	mu     sync.Mutex
	value  T
	subs   map[int]func(T)
	nextID int
}

// NewBehaviorSubject creates a subject holding initial as its current value.
func NewBehaviorSubject[T any](initial T) *BehaviorSubject[T] {
	return &BehaviorSubject[T]{value: initial, subs: make(map[int]func(T))}
}

// Value returns the subject's current value.
func (s *BehaviorSubject[T]) Value() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

// Next sets the current value and delivers it to every subscriber.
// Callbacks run synchronously on the caller's goroutine but outside the
// subject's lock, so a callback may itself call Next or Value; under
// concurrent Next calls the delivery order is unspecified — the current
// value, not the callback argument, is authoritative.
func (s *BehaviorSubject[T]) Next(v T) {
	s.mu.Lock()
	s.value = v
	fns := make([]func(T), 0, len(s.subs))
	for _, fn := range s.subs {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(v)
	}
}

// Subscribe registers fn for every future emission and returns its cancel.
// The current value is not replayed — read it with Value. Cancel is
// idempotent and safe to call concurrently with Next; a delivery already in
// flight may still land after cancel returns.
func (s *BehaviorSubject[T]) Subscribe(fn func(T)) (cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.subs[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}

// Subscription is one declared binding from a subject to an interactive
// component, built with Observe and returned from Subscriptions. It is
// inert on its own: the framework activates it after the render registers
// the live instance, and cancels it when the session's registry entry goes
// (D20) — components never manage the underlying subscription themselves.
type Subscription struct {
	// subscribe registers notify for the subject's emissions, returning the
	// cancel. Type-erased so one live instance may observe subjects of
	// different element types.
	subscribe func(notify func()) (cancel func())
	// apply pulls the subject's current value into the component. The pump
	// runs it under the session's dispatch mutex just before re-rendering,
	// so rapid emissions coalesce into a render of the latest state.
	apply func()
}

// Observe declares that an interactive component follows a subject: every
// emission runs apply (typically assigning to a field) and pushes the
// re-rendered component over the session's SSE stream (D3). apply receives
// the subject's current value at render time, which under rapid emissions
// may skip intermediate values — pushes carry latest state, not history
// (D20).
func Observe[T any](s *BehaviorSubject[T], apply func(T)) Subscription {
	return Subscription{
		subscribe: func(notify func()) func() {
			return s.Subscribe(func(T) { notify() })
		},
		apply: func() { apply(s.Value()) },
	}
}

// SubscriptionProvider is implemented by interactive components that follow
// subjects. Subscriptions runs on the fresh per-request instance after
// OnInit; the returned bindings live exactly as long as the instance's
// registry entry.
type SubscriptionProvider interface {
	Subscriptions() []Subscription
}

// subscriberCount reports the live subscriber count — the leak-check hook
// for white-box tests.
func (s *BehaviorSubject[T]) subscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}
