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

// copyFixtureDir copies every file in src into dst.
func copyFixtureDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	for _, e := range entries {
		data, readErr := os.ReadFile(filepath.Join(src, e.Name()))
		if readErr != nil {
			t.Fatalf("reading fixture file %s: %v", e.Name(), readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); writeErr != nil {
			t.Fatalf("copying fixture file %s: %v", e.Name(), writeErr)
		}
	}
}

func TestBuildThenServeRendersTheFixtureEndToEnd(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "hello"), dir)

	if err := run([]string{"build", dir}); err != nil {
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
