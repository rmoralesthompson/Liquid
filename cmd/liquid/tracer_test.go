package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// compiledHello pairs the fixture's struct shape with the template text that
// liquid build actually generated, joining the compiler seam to the runtime
// seam without hand-written markup.
type compiledHello struct {
	Name string
	text string
}

func (c *compiledHello) Selector() string { return "app-hello" }

func (c *compiledHello) Template() string { return c.text }

// copyFixtureDir copies the fixture tree rooted at src into dst, including
// any liquidstub module a fixture's go.mod replace points at.
func copyFixtureDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copying fixture %s: %v", src, err)
	}
}

func TestBuildThenServeRendersTheFixtureEndToEnd(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "hello"), dir)

	if err := run([]string{"build", dir}, io.Discard); err != nil {
		t.Fatalf("liquid build: %v", err)
	}

	app := liquid.New()
	c := &compiledHello{Name: "world", text: generatedTemplateText(t, filepath.Join(dir, "hello_gen.go"))}
	if err := app.Route("/", c); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if want := `<h1 title="world">Hello, world!</h1>`; !strings.Contains(string(body), want) {
		t.Errorf("end-to-end body = %q, want it to contain %q", body, want)
	}
}

// counterGen holds what liquid build generated for the counter fixture. The
// values live outside compiledCounter because a prototype's reference-typed
// fields must be nil at registration.
var counterGen struct {
	text    string
	actions []string
}

// compiledCounter pairs the counter fixture's struct shape with the compiler
// output, joining the compiler seam to the hydro runtime seam.
type compiledCounter struct {
	HydroID string
	Count   int
}

func (c *compiledCounter) Selector() string { return "app-counter" }

func (c *compiledCounter) Template() string { return counterGen.text }

func (c *compiledCounter) Actions() []string { return counterGen.actions }

// Increment mirrors the fixture's (click) handler.
func (c *compiledCounter) Increment() { c.Count++ }

func TestBuildThenClickRoundTripsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "counter"), dir)

	if err := run([]string{"build", dir}, io.Discard); err != nil {
		t.Fatalf("liquid build: %v", err)
	}
	genPath := filepath.Join(dir, "counter_gen.go")
	counterGen.text = generatedTemplateText(t, genPath)
	counterGen.actions = generatedActions(t, genPath)

	app := liquid.New()
	if err := app.Route("/", &compiledCounter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	h := liquidtest.New(t, app)
	page := h.Get("/")
	if got := page.Text("#count"); got != "0" {
		t.Fatalf(`initial render Text("#count") = %q, want "0"`, got)
	}
	if got := page.Fire("Increment").Text("#count"); got != "1" {
		t.Errorf(`patch Text("#count") = %q, want "1"`, got)
	}
}

// nestedGen holds what liquid build generated for the nested fixture's three
// components.
var nestedGen struct {
	dashboardText string
	userCardText  string
	avatarText    string
}

// compiledDashboard pairs the nested fixture's parent struct with the
// compiler output, joining the child-selector compile seam to the runtime's
// component registry.
type compiledDashboard struct {
	Title string
	Owner string
}

func (c *compiledDashboard) Selector() string { return "app-dashboard" }

func (c *compiledDashboard) Template() string { return nestedGen.dashboardText }

// compiledUserCard pairs the nested fixture's child struct with the compiler
// output.
type compiledUserCard struct {
	Name string
}

func (c *compiledUserCard) Selector() string { return "app-user-card" }

func (c *compiledUserCard) Template() string { return nestedGen.userCardText }

// compiledAvatar pairs the nested fixture's grandchild struct with the
// compiler output.
type compiledAvatar struct {
	Initials string
}

func (c *compiledAvatar) Selector() string { return "app-avatar" }

func (c *compiledAvatar) Template() string { return nestedGen.avatarText }

func TestBuildThenServeRendersNestedChildrenEndToEnd(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "nested"), dir)

	if err := run([]string{"build", dir}, io.Discard); err != nil {
		t.Fatalf("liquid build: %v", err)
	}
	nestedGen.dashboardText = generatedTemplateText(t, filepath.Join(dir, "dashboard_gen.go"))
	nestedGen.userCardText = generatedTemplateText(t, filepath.Join(dir, "user_card_gen.go"))
	nestedGen.avatarText = generatedTemplateText(t, filepath.Join(dir, "avatar_gen.go"))

	app := liquid.New()
	if err := app.Register(&compiledAvatar{}); err != nil {
		t.Fatalf("Register(avatar): %v", err)
	}
	if err := app.Register(&compiledUserCard{}); err != nil {
		t.Fatalf("Register(userCard): %v", err)
	}
	if err := app.Route("/", &compiledDashboard{Title: "Ops", Owner: "Ada"}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	page := liquidtest.New(t, app).Get("/")
	if page.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	if got := page.Text("h1"); got != "Ops" {
		t.Errorf(`parent render Text("h1") = %q, want "Ops"`, got)
	}
	if got := page.Text(".owner"); got != "Ada" {
		t.Errorf(`child render Text(".owner") = %q, want "Ada" — the compiled [input] binding must reach the child`, got)
	}
	if got := page.Text(".avatar"); got != "Ada" {
		t.Errorf(`grandchild render Text(".avatar") = %q, want "Ada" — compiled inputs must flow through recursive child renders`, got)
	}
}

// renamerGen holds what liquid build generated for the renamer fixture.
var renamerGen struct {
	text    string
	actions []string
}

// compiledRenamer pairs the renamer fixture's struct shape with the compiler
// output, joining the (submit)/CSRF compile seam to the hydro runtime seam.
type compiledRenamer struct {
	HydroID   string
	CSRFToken string
	Title     string
}

func (c *compiledRenamer) Selector() string { return "app-renamer" }

func (c *compiledRenamer) Template() string { return renamerGen.text }

func (c *compiledRenamer) Actions() []string { return renamerGen.actions }

// Rename mirrors the fixture's (submit) handler.
func (c *compiledRenamer) Rename(e liquid.Event) { c.Title = e.String("title") }

func TestBuildThenSubmitRoundTripsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "renamer"), dir)

	if err := run([]string{"build", dir}, io.Discard); err != nil {
		t.Fatalf("liquid build: %v", err)
	}
	genPath := filepath.Join(dir, "renamer_gen.go")
	renamerGen.text = generatedTemplateText(t, genPath)
	renamerGen.actions = generatedActions(t, genPath)

	app := liquid.New()
	if err := app.Route("/", &compiledRenamer{Title: "Untitled"}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	h := liquidtest.New(t, app)
	page := h.Get("/")
	if got := page.Text("#title"); got != "Untitled" {
		t.Fatalf(`initial render Text("#title") = %q, want "Untitled"`, got)
	}
	token := page.CSRFToken()
	if token == "" {
		t.Fatal("rendered page exposes no CSRF token")
	}
	if want := `name="csrf_token" value="` + token + `"`; !strings.Contains(page.Body, want) {
		t.Errorf("compiled form did not render the populated CSRF input %q\n--- body ---\n%s", want, page.Body)
	}

	if got := page.Fire("Rename", liquidtest.Field("title", "Ops")).Text("#title"); got != "Ops" {
		t.Errorf(`patch Text("#title") = %q, want "Ops"`, got)
	}
}

// generatedActions extracts the string literals returned by the Actions
// method in a *_gen.go file — the compiled allowlist, exactly as generated.
func generatedActions(t *testing.T, genPath string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, genPath, nil, 0)
	if err != nil {
		t.Fatalf("parsing generated file: %v", err)
	}
	var actions []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Actions" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			unquoted, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquoting action literal: %v", err)
			}
			actions = append(actions, unquoted)
			return true
		})
		return false
	})
	if len(actions) == 0 {
		t.Fatalf("no Actions allowlist found in %s", genPath)
	}
	return actions
}

// generatedTemplateText extracts the string literal returned by the Template
// method in a *_gen.go file.
func generatedTemplateText(t *testing.T, genPath string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, genPath, nil, 0)
	if err != nil {
		t.Fatalf("parsing generated file: %v", err)
	}
	var text string
	ast.Inspect(f, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		lit, ok := ret.Results[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		unquoted, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquoting template literal: %v", err)
		}
		text = unquoted
		return false
	})
	if text == "" {
		t.Fatalf("no template string literal found in %s", genPath)
	}
	return text
}
