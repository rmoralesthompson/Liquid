package ui

// Hello is the dev-loop fixture component.
type Hello struct{}

// Selector returns the custom-element tag this component renders as.
func (h *Hello) Selector() string { return "app-hello" }
