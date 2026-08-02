package liquid_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// newAppServer serves an already-configured App for the test's lifetime.
func newAppServer(t *testing.T, app *liquid.App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

// greeter is a singleton service registered by its concrete pointer type.
type greeter struct {
	Greeting string
}

type usesGreeter struct {
	Svc  *greeter `inject:""`
	Name string
}

func (u *usesGreeter) Selector() string { return "app-uses-greeter" }

func (u *usesGreeter) Template() string { return "<p>{{ .Svc.Greeting }}, {{ .Name }}!</p>" }

func TestProvidedServiceIsInjectedByConcreteType(t *testing.T) {
	app := liquid.New()
	if err := app.Provide(&greeter{Greeting: "Hello"}); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &usesGreeter{Name: "world"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	resp, body := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %q)", resp.StatusCode, http.StatusOK, body)
	}
	if want := "<p>Hello, world!</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q — the provided service must reach the inject-tagged field", body, want)
	}
}

// Greeting is the interface seam a component depends on; the registered
// concrete service satisfies it.
type Greeting interface {
	Greet() string
}

func (g *greeter) Greet() string { return g.Greeting }

type usesGreeting struct {
	Svc Greeting `inject:""`
}

func (u *usesGreeting) Selector() string { return "app-uses-greeting" }

func (u *usesGreeting) Template() string { return "<p>{{ .Svc.Greet }}</p>" }

func TestProvidedServiceIsInjectedIntoInterfaceField(t *testing.T) {
	app := liquid.New()
	if err := app.Provide(&greeter{Greeting: "Hi"}); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &usesGreeting{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := newAppServer(t, app)

	_, body := get(t, srv.URL+"/")

	if want := "<p>Hi</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q — a service implementing the field's interface must be injected", body, want)
	}
}

func TestMissingDependencyFailsAtRegistrationNotAsANilField(t *testing.T) {
	app := liquid.New() // nothing provided

	err := app.Route("/", &usesGreeter{})

	if err == nil {
		t.Fatal("Route accepted a component whose inject-tagged field no provided service satisfies; want a hard registration error (D8)")
	}
	if want := "usesGreeter.Svc"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to name the unsatisfied field %q", err, want)
	}
}

func TestAmbiguousInterfaceDependencyFailsAtRegistration(t *testing.T) {
	app := liquid.New()
	if err := app.Provide(&greeter{Greeting: "Hi"}); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Provide(&loudGreeter{}); err != nil {
		t.Fatalf("Provide: %v", err)
	}

	err := app.Route("/", &usesGreeting{})

	if err == nil {
		t.Fatal("Route accepted an interface dependency two provided services satisfy; want an ambiguity error, not a silent pick")
	}
	for _, want := range []string{"liquid_test.greeter", "liquid_test.loudGreeter"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name candidate %q", err, want)
		}
	}
}

type loudGreeter struct{}

func (l *loudGreeter) Greet() string { return "HI" }

type unexportedInject struct {
	svc *greeter `inject:""` //nolint:unused // the tag itself is what's under test
}

func (u *unexportedInject) Selector() string { return "app-unexported-inject" }

func (u *unexportedInject) Template() string { return placeholderHTML }

func TestInjectTagOnUnexportedFieldFailsAtRegistration(t *testing.T) {
	app := liquid.New()
	if err := app.Provide(&greeter{Greeting: "Hi"}); err != nil {
		t.Fatalf("Provide: %v", err)
	}

	if err := app.Route("/", &unexportedInject{}); err == nil {
		t.Error("Route accepted an inject tag on an unexported field; the runtime cannot set it and must say so at registration")
	}
}

func TestProvideRejectsNil(t *testing.T) {
	app := liquid.New()

	if err := app.Provide(nil); err == nil {
		t.Error("Provide accepted nil; a nil service can never satisfy a dependency and must be rejected loudly")
	}
	if err := app.Provide((*greeter)(nil)); err == nil {
		t.Error("Provide accepted a typed nil pointer; it would inject as a nil field — exactly what D8's hard-error contract forbids")
	}
}
