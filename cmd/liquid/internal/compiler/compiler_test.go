package compiler_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"html/template"
	"os"
	"path/filepath"
	"reflect"
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

// build runs compiler.Build and fails the test on a mechanical (non-diagnostic)
// error, returning any diagnostics for the caller to assert on.
func build(t *testing.T, dir string) []compiler.Diagnostic {
	t.Helper()
	diags, err := compiler.Build(context.Background(), dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return diags
}

func TestBuildReportsMissingPairedSourceAsDiagnostic(t *testing.T) {
	dir := copyFixture(t, "orphan")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "orphan.lsx"),
		Line:       1,
		Col:        1,
		Severity:   compiler.SeverityError,
		Code:       "LSX002",
		Message:    "no paired Go source file for orphan.lsx: orphan.go does not exist",
		Suggestion: "create orphan.go defining a struct named Orphan",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "orphan_gen.go")); !os.IsNotExist(err) {
		t.Errorf("orphan_gen.go must not be written for a broken pairing (stat err: %v)", err)
	}
}

func TestBuildReportsUnclosedInterpolationWithPosition(t *testing.T) {
	dir := copyFixture(t, "unclosed")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "unclosed.lsx"),
		Line:       3,
		Col:        6,
		Severity:   compiler.SeverityError,
		Code:       "LSX001",
		Message:    "unclosed interpolation: {{ has no matching }}",
		Suggestion: "close the interpolation with }}",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "unclosed_gen.go")); !os.IsNotExist(err) {
		t.Errorf("unclosed_gen.go must not be written for malformed input (stat err: %v)", err)
	}
}

func TestBuildReportsUnknownFieldReferenceWithSuggestion(t *testing.T) {
	dir := copyFixture(t, "typo")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "typo.lsx"),
		Line:       3,
		Col:        10,
		Severity:   compiler.SeverityError,
		Code:       "LSX004",
		Message:    "Typo has no field or method named Nmae",
		Suggestion: "did you mean Name?",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "typo_gen.go")); !os.IsNotExist(err) {
		t.Errorf("typo_gen.go must not be written when a reference fails vet (stat err: %v)", err)
	}
}

func TestBuildReportsMissingPairedStructAsDiagnostic(t *testing.T) {
	dir := copyFixture(t, "mismatch")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "mismatch.lsx"),
		Line:       1,
		Col:        1,
		Severity:   compiler.SeverityError,
		Code:       "LSX003",
		Message:    "package mismatch does not define a struct named Mismatch to pair with mismatch.lsx",
		Suggestion: "define type Mismatch struct in mismatch.go, or rename the files to match an existing struct",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "mismatch_gen.go")); !os.IsNotExist(err) {
		t.Errorf("mismatch_gen.go must not be written for a broken pairing (stat err: %v)", err)
	}
}

func TestVetReportsDiagnosticsWithoutWritingFiles(t *testing.T) {
	dir := copyFixture(t, "typo")

	diags, err := compiler.Vet(context.Background(), dir)
	if err != nil {
		t.Fatalf("Vet: %v", err)
	}

	if len(diags) != 1 || diags[0].Code != "LSX004" {
		t.Errorf("diagnostics = %+v, want exactly one LSX004", diags)
	}
	if _, err := os.Stat(filepath.Join(dir, "typo_gen.go")); !os.IsNotExist(err) {
		t.Errorf("vet must not write generated files (stat err: %v)", err)
	}
}

func TestVetOnCleanInputReportsNothingAndWritesNothing(t *testing.T) {
	dir := copyFixture(t, "hello")

	diags, err := compiler.Vet(context.Background(), dir)
	if err != nil {
		t.Fatalf("Vet: %v", err)
	}

	if len(diags) != 0 {
		t.Errorf("diagnostics = %+v, want none", diags)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello_gen.go")); !os.IsNotExist(err) {
		t.Errorf("vet must not write generated files even for clean input (stat err: %v)", err)
	}
}

func TestBuildGeneratesTemplateMethodFromPairedLSX(t *testing.T) {
	dir := copyFixture(t, "hello")

	if diags := build(t, dir); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
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

	if diags := build(t, dir); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
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
