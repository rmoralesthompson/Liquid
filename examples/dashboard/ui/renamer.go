package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// Renamer is the form card: a (submit) handler behind the auto-injected
// CSRF token (D12/D15). The board name it renames is its own state — the
// parent seeds it once through the [name] input.
type Renamer struct {
	// HydroID marks the card interactive.
	HydroID string
	// CSRFToken backs the hidden csrf_token input the compiler injects into
	// every form.
	CSRFToken string
	// Name is the current board name.
	Name string
}

// Selector returns the custom element tag for the component.
func (r *Renamer) Selector() string { return "app-renamer" }

// Rename handles the rename form; a blank submission keeps the old name.
func (r *Renamer) Rename(e liquid.Event) {
	if name := e.String("name"); name != "" {
		r.Name = name
	}
}
