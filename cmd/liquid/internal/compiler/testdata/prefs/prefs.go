package prefs

import liquid "github.com/rmoralesthompson/liquid/core"

// Level is a closed set of priority levels (D30): a named type backed by a
// typed const block — the idiomatic Go enum the compiler enumerates when it can
// see the payload struct.
type Level string

// The levels a SetPriority may carry.
const (
	Low  Level = "low"
	High Level = "high"
)

// SetPriorityPayload is what the author *intends* SetPriority to receive: a
// closed-domain Level. But because SetPriority declares no guard, this struct
// is never named to the compiler (the handler takes liquid.Event), so the
// closed domain is not enumerated and the seam does not enforce it. This
// fixture pins that documented v0.1 gap (#85, ADR-0003): a closed-domain field
// is enforced only when the action declares a guard.
type SetPriorityPayload struct {
	Level Level
}

// Prefs sets a priority level.
type Prefs struct {
	HydroID string
	Current string
}

// Selector returns the custom element tag for the component.
func (c *Prefs) Selector() string { return "app-prefs" }

// SetPriority takes a client payload but declares no SetPriorityGuard, so
// nothing constrains Level at the dispatch seam despite it being a closed
// domain — the footgun #85 documents.
func (c *Prefs) SetPriority(e liquid.Event) { c.Current = e.String("level") }
