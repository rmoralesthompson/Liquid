package ergobench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures below are each task's known-good output, verified against the
// real compiler (build / vet / manifest --json) so the scripted Layer 1 tests
// pin the exact loop behavior a regression must preserve, with no LLM.

// --- input-wiring: parent nests child and binds Name via [input] ---

func dashboardGo() File {
	return File{Name: "dashboard.go", Content: `package widgets

// Dashboard is the parent, passing its Owner down as the child's Name.
type Dashboard struct {
	Owner string
}

// Selector returns the custom element tag.
func (c *Dashboard) Selector() string { return "app-dashboard" }
`}
}

func userCardFiles() []File {
	return []File{
		{Name: "user_card.go", Content: `package widgets

// UserCard is the child, receiving its Name by [input].
type UserCard struct {
	Name string
}

// Selector returns the custom element tag.
func (c *UserCard) Selector() string { return "app-user-card" }
`},
		{Name: "user_card.lsx", Content: `<div class="card">{{ Name }}</div>
`},
	}
}

func inputWiringFixed() []File {
	return append([]File{
		dashboardGo(),
		{Name: "dashboard.lsx", Content: `<section><app-user-card [name]="Owner"></app-user-card></section>
`},
	}, userCardFiles()...)
}

// inputWiringNoBinding nests the child but omits the [name] binding, so the
// child's Name is never bound via [input]: it builds clean yet the manifest
// input flag is false, a spec miss.
func inputWiringNoBinding() []File {
	return append([]File{
		dashboardGo(),
		{Name: "dashboard.lsx", Content: `<section><app-user-card></app-user-card></section>
`},
	}, userCardFiles()...)
}

func TestInputWiringFirstPassClean(t *testing.T) {
	gen := NewScriptedGenerator(inputWiringFixed())
	s, err := Run(context.Background(), gen, InputWiring, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Sample{FirstPassClean: true, Repairs: 0, ReachedGreen: true, SpecMatch: true}
	if s != want {
		t.Errorf("sample = %+v, want %+v", s, want)
	}
}

func TestInputWiringMissingBindingIsSpecMiss(t *testing.T) {
	gen := NewScriptedGenerator(inputWiringNoBinding())
	s, err := Run(context.Background(), gen, InputWiring, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.ReachedGreen {
		t.Fatalf("component must build clean; sample = %+v", s)
	}
	if s.SpecMatch {
		t.Error("SpecMatch = true, want false: child Name is not bound via [input]")
	}
}

func TestInputWiringSpecMissReasonIsInputFlag(t *testing.T) {
	dir := t.TempDir()
	if err := writeModule(dir, InputWiring, inputWiringNoBinding()); err != nil {
		t.Fatalf("writeModule: %v", err)
	}
	ok, reason := specMatch(context.Background(), dir, InputWiring.Expect)
	if ok {
		t.Fatal("specMatch = true, want false")
	}
	if reason != `field "Name" input = false, want true` {
		t.Errorf("reason = %q, want the input-flag mismatch", reason)
	}
}

// --- managed-observe: follow an observable via the managed Observe path ---

func managedFixed() []File {
	return []File{
		{Name: "managed.go", Content: `package managed

import liquid "github.com/rmoralesthompson/liquid/core"

// Managed follows an observable through the framework-owned Observe path, so
// the D29 leak check leaves it alone.
type Managed struct {
	HydroID string
	Count   int
	orders  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag.
func (c *Managed) Selector() string { return "app-managed" }

// Subscriptions declares the framework-owned binding; the framework cancels it
// on session GC, so there is nothing for the component to unsubscribe.
func (c *Managed) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(c.orders, func(n int) { c.Count = n }),
	}
}
`},
		{Name: "managed.lsx", Content: `<div [hydroId]>{{ Count }}</div>
`},
	}
}

func TestManagedObserveFirstPassClean(t *testing.T) {
	gen := NewScriptedGenerator(managedFixed())
	s, err := Run(context.Background(), gen, ManagedObserve, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Sample{FirstPassClean: true, Repairs: 0, ReachedGreen: true, SpecMatch: true}
	if s != want {
		t.Errorf("sample = %+v, want %+v", s, want)
	}
}

// --- guardrail-trap: raw Subscribe (LSX017) steered to the managed path ---

// trapLeaky is the trap's obvious-but-wrong first attempt: Subscribe in OnInit
// discards its cancel, so the D29 check errors (LSX017) and the loop must
// repair.
func trapLeaky() []File {
	return []File{
		{Name: "live.go", Content: `package live

import liquid "github.com/rmoralesthompson/liquid/core"

// Live keeps Count in sync with an observable.
type Live struct {
	HydroID string
	Count   int
	prices  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag.
func (c *Live) Selector() string { return "app-live" }

// OnInit subscribes directly and discards the cancel — the trap.
func (c *Live) OnInit() {
	c.prices.Subscribe(func(n int) { c.Count = n })
}
`},
		{Name: "live.lsx", Content: `<div [hydroId]>{{ Count }}</div>
`},
	}
}

// trapFixed is the repaired attempt: the same component follows the observable
// through the managed Observe path the LSX017 suggestion points to.
func trapFixed() []File {
	return []File{
		{Name: "live.go", Content: `package live

import liquid "github.com/rmoralesthompson/liquid/core"

// Live keeps Count in sync with an observable.
type Live struct {
	HydroID string
	Count   int
	prices  *liquid.BehaviorSubject[int]
}

// Selector returns the custom element tag.
func (c *Live) Selector() string { return "app-live" }

// Subscriptions declares the framework-owned binding, so nothing leaks.
func (c *Live) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(c.prices, func(n int) { c.Count = n }),
	}
}
`},
		{Name: "live.lsx", Content: `<div [hydroId]>{{ Count }}</div>
`},
	}
}

func TestGuardrailTrapRepairsToGreen(t *testing.T) {
	// Attempt 0 trips LSX017; attempt 1 follows the suggestion to Observe.
	gen := NewScriptedGenerator(trapLeaky(), trapFixed())
	s, err := Run(context.Background(), gen, GuardrailTrap, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Sample{FirstPassClean: false, Repairs: 1, ReachedGreen: true, SpecMatch: true}
	if s != want {
		t.Errorf("sample = %+v, want %+v", s, want)
	}
}

// --- scaffold: NeedsCore lays down the replace directive and stub ---

func TestNeedsCoreScaffoldWritesStub(t *testing.T) {
	dir := t.TempDir()
	if err := writeModule(dir, ManagedObserve, managedFixed()); err != nil {
		t.Fatalf("writeModule: %v", err)
	}

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	if !strings.Contains(string(gomod), "replace github.com/rmoralesthompson/liquid => ./liquidstub") {
		t.Errorf("go.mod missing core replace directive:\n%s", gomod)
	}

	if _, err := os.Stat(filepath.Join(dir, "liquidstub", "core", "liquid.go")); err != nil {
		t.Errorf("core stub not written: %v", err)
	}
}

func TestBareTaskScaffoldOmitsStub(t *testing.T) {
	dir := t.TempDir()
	if err := writeModule(dir, InputWiring, inputWiringFixed()); err != nil {
		t.Fatalf("writeModule: %v", err)
	}

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	if strings.Contains(string(gomod), "replace") {
		t.Errorf("bare task go.mod should have no replace directive:\n%s", gomod)
	}
	if _, err := os.Stat(filepath.Join(dir, "liquidstub")); !os.IsNotExist(err) {
		t.Errorf("bare task should not write a stub; stat err = %v", err)
	}
}
