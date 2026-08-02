package compiler_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// copyFixture copies a testdata fixture project into a temp dir so Build can
// write generated files without touching the repo.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", name)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("reading fixture file %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("copying fixture file %s: %v", e.Name(), err)
		}
	}
	return dst
}

func TestBuildGeneratesTemplateMethodFromPairedLSX(t *testing.T) {
	dir := copyFixture(t, "hello")

	if err := compiler.Build(dir); err != nil {
		t.Fatalf("Build: %v", err)
	}

	generated, err := os.ReadFile(filepath.Join(dir, "hello_gen.go"))
	if err != nil {
		t.Fatalf("expected hello_gen.go beside the source: %v", err)
	}
	got := string(generated)

	for _, want := range []string{
		"package hello",
		"func (c *Hello) Template() string",
		"Hello, {{ .Name }}!",
		`title=\"{{ .Name }}\"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file missing %q\n--- generated ---\n%s", want, got)
		}
	}
}

func TestGeneratedTemplateExecutesAsHTMLTemplate(t *testing.T) {
	dir := copyFixture(t, "hello")

	if err := compiler.Build(dir); err != nil {
		t.Fatalf("Build: %v", err)
	}

	tmplText := generatedTemplateText(t, filepath.Join(dir, "hello_gen.go"))
	tmpl, err := template.New("hello").Parse(tmplText)
	if err != nil {
		t.Fatalf("generated text is not valid html/template: %v\n--- text ---\n%s", err, tmplText)
	}

	var b strings.Builder
	if err := tmpl.Execute(&b, struct{ Name string }{Name: "world"}); err != nil {
		t.Fatalf("executing generated template: %v", err)
	}
	if got, want := b.String(), `<h1 title="world">Hello, world!</h1>`; got != want {
		t.Errorf("rendered output = %q, want %q", got, want)
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
