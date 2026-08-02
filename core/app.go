package liquid

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"reflect"
)

// App routes HTTP requests to components. Component templates are parsed
// once, at registration; every request renders a fresh component instance —
// registered instances act only as prototypes and are never shared mutable
// state across requests.
type App struct {
	logger *slog.Logger
	routes map[string]*route
}

type route struct {
	prototype reflect.Value // pointer to the registered component struct
	tmpl      *template.Template
}

// Option configures an App at construction.
type Option func(*App)

// WithLogger sets the slog logger the App uses for runtime errors. Without
// it, the App logs through slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(a *App) { a.logger = l }
}

// New creates an App, applying any options.
func New(opts ...Option) *App {
	a := &App{
		logger: slog.Default(),
		routes: make(map[string]*route),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Route registers a component to serve GET requests at path. The component's
// template is parsed immediately; a template error is reported here, at
// registration, never at request time. The instance passed in is a prototype:
// its field values seed each per-request copy, so its reference-typed fields
// (slices, maps, pointers, …) must be nil — a shallow copy of a live
// reference would be shared mutable state across requests. Per-request data
// belongs in lifecycle hooks, not the prototype.
func (a *App) Route(path string, c Component) error {
	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("registering route %s: component must be a pointer to a struct, got %T", path, c)
	}
	if err := validatePrototype(v.Elem(), v.Elem().Type().Name()); err != nil {
		return fmt.Errorf("registering route %s: %w", path, err)
	}

	tmpl, err := template.New(c.Selector()).Parse(c.Template())
	if err != nil {
		return fmt.Errorf("parsing template for %s: %w", c.Selector(), err)
	}

	a.routes[path] = &route{prototype: v, tmpl: tmpl}
	return nil
}

// validatePrototype rejects prototype structs holding non-nil reference
// values, walking nested structs. Nil reference fields are fine: they copy
// as nil and each request may populate its own.
func validatePrototype(structV reflect.Value, path string) error {
	t := structV.Type()
	for i := range structV.NumField() {
		f := structV.Field(i)
		name := path + "." + t.Field(i).Name
		switch f.Kind() {
		case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
			if !f.IsNil() {
				return fmt.Errorf("prototype field %s holds a non-nil %s, which a per-request copy would share across requests; leave it nil", name, f.Kind())
			}
		case reflect.Struct:
			if err := validatePrototype(f, name); err != nil {
				return err
			}
		default:
		}
	}
	return nil
}

// ServeHTTP implements http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rt, ok := a.routes[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}

	inst := reflect.New(rt.prototype.Elem().Type())
	inst.Elem().Set(rt.prototype.Elem())

	var buf bytes.Buffer
	if err := rt.tmpl.Execute(&buf, inst.Interface()); err != nil {
		a.logger.Error("rendering component", "path", r.URL.Path, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := buf.WriteTo(w); err != nil {
		a.logger.Error("writing response", "path", r.URL.Path, "error", err)
	}
}
