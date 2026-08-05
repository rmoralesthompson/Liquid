// Package liquid is a fixture stub standing in for the real core package:
// vet needs the observable types and Observe to resolve at their real import
// path so the D29 reactivity-leak check can identify Subscribe calls. It
// carries no behavior.
package liquid

// Observable is the read-and-subscribe surface shared by BehaviorSubject and
// the derived combinators (D25).
type Observable[T any] interface {
	Value() T
	Subscribe(fn func(T)) (cancel func())
}

// BehaviorSubject is observable state with a current value (D3).
type BehaviorSubject[T any] struct{ v T }

// NewBehaviorSubject creates a subject holding initial as its current value.
func NewBehaviorSubject[T any](initial T) *BehaviorSubject[T] { return &BehaviorSubject[T]{v: initial} }

// Value returns the subject's current value.
func (s *BehaviorSubject[T]) Value() T { return s.v }

// Next sets the current value.
func (s *BehaviorSubject[T]) Next(v T) { s.v = v }

// Subscribe registers fn for future emissions and returns its cancel.
func (s *BehaviorSubject[T]) Subscribe(fn func(T)) (cancel func()) { return func() {} }

// Derived is a value computed from one or more upstream Observables (D25).
type Derived[T any] struct{ v T }

// Value returns the derived value's current value.
func (d *Derived[T]) Value() T { return d.v }

// Subscribe follows the derived value's emissions, exactly as for a subject.
func (d *Derived[T]) Subscribe(fn func(T)) (cancel func()) { return func() {} }

// Map is a 1→1 projection over an observable (D25).
func Map[T, U any](src Observable[T], fn func(T) U) *Derived[U] { return &Derived[U]{} }

// Subscription is one framework-owned binding declared with Observe.
type Subscription struct{}

// Observe declares that an interactive component follows an observable; the
// framework owns the subscription lifecycle (D25).
func Observe[T any](src Observable[T], apply func(T)) Subscription { return Subscription{} }
