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

// Initializer is implemented by components that load per-request state
// before render (D18). OnInit runs on the fresh per-request instance, after
// path-param binding and guards; an error return produces the framework
// error page instead of a render.
type Initializer interface {
	OnInit(ctx Ctx) error
}
