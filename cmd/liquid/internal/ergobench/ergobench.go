// Package ergobench is the Tier B agent-ergonomics harness (ADR-0001, #71): it
// drives the generate → build → feed-diagnostics-back → repair loop against a
// task corpus and scores the result with the manifest structural oracle, with
// no human in the scoring loop.
//
// The harness is deliberately split from the model. The loop, the oracle, and
// the scoring here (Layer 1) are deterministic given a deterministic
// [Generator], so they are exercised on every PR with a scripted generator and
// zero LLM. The live LLM generator (Layer 2) plugs into the same [Generator]
// interface and runs nightly/on-demand behind a build tag — it never touches
// the PR critical path, because a real model is stochastic and costly (ADR-0001,
// "Two tiers").
//
// Spec-match reuses primitives that already exist: it runs the same
// compiler.Manifest surface `liquid manifest --json` exposes over the emitted
// component and compares the structural facts (selector, interactivity, fields,
// actions) against the task's [Expectation]. No rendered-DOM diff yet — that
// second oracle (#60 snapshots) is a follow-up.
package ergobench

import (
	"context"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// File is one emitted source file, named relative to the component module root
// (e.g. "counter.lsx", "counter.go"). The harness scaffolds the surrounding
// go.mod; a Generator emits only the component's own files, exactly as an agent
// editing an existing project would.
type File struct {
	Name    string
	Content string
}

// Generator emits component source for a task. On the first attempt prior and
// diags are nil; on a repair attempt prior carries the files the generator last
// emitted and diags carries the diagnostics their build produced — the D13
// findings the generator is asked to fix. The live LLM generator and the
// deterministic [ScriptedGenerator] both satisfy this one seam.
type Generator interface {
	Generate(ctx context.Context, prompt string, prior []File, diags []compiler.Diagnostic) ([]File, error)
}

// Task is one corpus entry: the prompt handed to the generator, the structural
// answer key its green output is scored against, and the repair give-up bound.
type Task struct {
	// Name identifies the task and seeds the scaffolded module path.
	Name string
	// Prompt is the instruction handed to the generator verbatim.
	Prompt string
	// Expect is the structural oracle for spec-match.
	Expect Expectation
	// CapN bounds repair iterations: after CapN failed repairs the sample is
	// scored as gave-up (ADR-0001: "capped at N, N = give-up").
	CapN int
}

// Expectation is the structural oracle a green output is scored against. Only
// the fields set here are asserted — a zero Interactive pointer, an empty
// Fields slice, and so on are "don't care", so a task pins exactly the surface
// its prompt asked for and nothing more.
type Expectation struct {
	// Selector names the component to score; it is also asserted to exist.
	Selector string
	// Interactive, when non-nil, asserts the component's [hydroId]-root flag.
	Interactive *bool
	// Fields are the struct fields that must be present (extras are allowed).
	Fields []FieldExpect
	// Actions are the allowlisted handlers that must be present (extras allowed).
	Actions []ActionExpect
}

// FieldExpect asserts one struct field is present, and optionally its type and
// input-ness. An empty Type and a nil Input are not asserted.
type FieldExpect struct {
	Name  string
	Type  string
	Input *bool
}

// ActionExpect asserts one allowlisted handler is present, and optionally its
// event shape. A nil TakesEvent is not asserted; Events lists binding kinds
// that must all be wired (extras allowed).
type ActionExpect struct {
	Name       string
	TakesEvent *bool
	Events     []string
}

// Sample is the outcome of running one task once through the loop.
type Sample struct {
	// FirstPassClean reports the first emitted attempt built clean.
	FirstPassClean bool
	// Repairs is the number of repair iterations used to reach green, or CapN
	// when the sample gave up.
	Repairs int
	// ReachedGreen reports a clean build within CapN repairs.
	ReachedGreen bool
	// SpecMatch reports the green output satisfied the task Expectation. It is
	// only meaningful when ReachedGreen is true.
	SpecMatch bool
}
