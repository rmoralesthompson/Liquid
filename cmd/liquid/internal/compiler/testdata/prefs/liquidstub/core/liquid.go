// Package liquid is a fixture stub standing in for the real core package:
// vet only needs the liquid.Event type to resolve at its real import path so
// handler signatures type-check. It carries no behavior.
package liquid

// Event is the handler payload type (D11).
type Event struct{}

// String returns the named payload field.
func (e Event) String(name string) string { return "" }
