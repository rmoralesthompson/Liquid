package mover

import liquid "github.com/rmoralesthompson/liquid/core"

// Direction is the closed set of directions a Move may carry (D30): a named
// type backed by a typed const block, the idiomatic Go enum the compiler
// enumerates.
type Direction string

// The directions a Move admits.
const (
	Up   Direction = "up"
	Down Direction = "down"
)

// Mover moves a marker up or down by a guarded step.
type Mover struct {
	HydroID string
	Pos     int
}

// Selector returns the custom element tag for the component.
func (c *Mover) Selector() string { return "app-mover" }

// MovePayload is Move's payload: a closed-domain direction and an unbounded
// scalar step the guard constrains.
type MovePayload struct {
	Dir  Direction
	Step int
}

// Move shifts the marker; it trusts the seam to have refused an out-of-domain
// Dir or a non-positive Step (D30).
func (c *Mover) Move(e liquid.Event) { c.Pos++ }

// MoveGuard is the D30 boundary guard: a pure predicate refusing a
// non-positive step before any effect fires.
func (c *Mover) MoveGuard(p MovePayload) bool { return p.Step > 0 }
