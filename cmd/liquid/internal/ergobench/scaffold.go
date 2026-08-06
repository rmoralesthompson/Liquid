package ergobench

import (
	"fmt"
	"os"
	"path/filepath"
)

// moduleGoMod returns the go.mod for a task's scaffolded module. By default it
// is a bare module — the greenfield, add-interactivity, and [input]-wiring
// skills all compile as plain structs plus templates. A task whose component
// imports liquid core (NeedsCore) additionally requires it and replaces it with
// the local stub, so the import resolves at its real path without a published
// core package (see liquidCoreStub).
func moduleGoMod(task Task) string {
	base := fmt.Sprintf("module ergobench.local/%s\n\ngo 1.23\n", task.Name)
	if !task.NeedsCore {
		return base
	}
	return base + "\nrequire github.com/rmoralesthompson/liquid v0.0.0\n\nreplace github.com/rmoralesthompson/liquid => ./liquidstub\n"
}

// writeLiquidStub materializes the liquid core stub as a nested module under
// dir/liquidstub, which the module's go.mod replaces the real core with. The
// caller writes that replace directive (see moduleGoMod); this only lays down
// the files.
func writeLiquidStub(dir string) error {
	coreDir := filepath.Join(dir, "liquidstub", "core")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		return fmt.Errorf("creating stub dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "liquidstub", "go.mod"), []byte(liquidStubGoMod), 0o644); err != nil {
		return fmt.Errorf("writing stub go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "liquid.go"), []byte(liquidCoreStub), 0o644); err != nil {
		return fmt.Errorf("writing stub source: %w", err)
	}
	return nil
}

// liquidStubGoMod is the module manifest the stub is served under; its path must
// match the real core so components resolve `github.com/rmoralesthompson/liquid/core`.
const liquidStubGoMod = "module github.com/rmoralesthompson/liquid\n\ngo 1.23\n"

// liquidCoreStub stands in for the real liquid/core package. A task that
// follows an observable (managed Observe, the guardrail trap) compiles against
// this stub rather than the repo's real core, so the harness stays
// deterministic and self-contained: a temp module needs no absolute path back
// to the checkout's core package to resolve the import vet and manifest read.
// It carries the observable surface (BehaviorSubject, Observe, Subscription)
// and no behavior, mirroring compiler/testdata's stub so the harness scores the
// same D25/D29 contract the real compiler enforces.
const liquidCoreStub = `// Package liquid is a fixture stub standing in for the real core package:
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
`
