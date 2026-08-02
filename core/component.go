// Package liquid is the Liquid runtime: the component model, router, and
// (in later slices) the hydro event loop. Components live and execute on the
// server; the browser receives rendered HTML.
package liquid

// Component is a server-side UI component. Exported struct fields are
// template-visible state; the template is .lsx markup, usually generated
// from a paired .lsx file by liquid build.
type Component interface {
	// Selector returns the component's custom element tag, e.g. "app-hello".
	Selector() string
	// Template returns the component's compiled .lsx markup.
	Template() string
}
