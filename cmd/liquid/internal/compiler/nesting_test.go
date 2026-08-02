package compiler_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

func TestChildSelectorCompilesToLiquidChildCall(t *testing.T) {
	dir := copyFixture(t, "nested")

	if diags := build(t, dir); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	tmplText := generatedTemplateText(t, filepath.Join(dir, "dashboard_gen.go"))

	if want := `{{liquidChild "app-user-card" "name" .Owner}}`; !strings.Contains(tmplText, want) {
		t.Errorf("generated template missing child call %q\n--- template ---\n%s", want, tmplText)
	}
	if strings.Contains(tmplText, "<app-user-card") {
		t.Errorf("child selector element must not survive into the generated template\n--- template ---\n%s", tmplText)
	}

	// The child nests a grandchild of its own — composition must compile at
	// every level, not just the routed root.
	cardText := generatedTemplateText(t, filepath.Join(dir, "user_card_gen.go"))
	if want := `{{liquidChild "app-avatar" "initials" .Name}}`; !strings.Contains(cardText, want) {
		t.Errorf("child's generated template missing grandchild call %q\n--- template ---\n%s", want, cardText)
	}

	if _, err := os.Stat(filepath.Join(dir, "avatar_gen.go")); err != nil {
		t.Errorf("expected avatar_gen.go for the grandchild component: %v", err)
	}
}

func TestBuildReportsUnknownChildSelectorWithSuggestion(t *testing.T) {
	dir := copyFixture(t, "ghostchild")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "ghost.lsx"),
		Line:       2,
		Col:        3,
		Severity:   compiler.SeverityError,
		Code:       "LSX012",
		Message:    "no component in package ghostchild declares the selector app-user-crd",
		Suggestion: "did you mean app-user-card?",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "ghost_gen.go")); !os.IsNotExist(err) {
		t.Errorf("ghost_gen.go must not be written for an unknown child selector (stat err: %v)", err)
	}
}

func TestBuildReportsUnknownChildFieldWithSuggestion(t *testing.T) {
	dir := copyFixture(t, "badinput")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "panel.lsx"),
		Line:       2,
		Col:        18,
		Severity:   compiler.SeverityError,
		Code:       "LSX013",
		Message:    "UserCard has no field named nmae for the [input] binding",
		Suggestion: "did you mean Name?",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "panel_gen.go")); !os.IsNotExist(err) {
		t.Errorf("panel_gen.go must not be written for a bad input binding (stat err: %v)", err)
	}
}

func TestBuildReportsUnassignableInputBinding(t *testing.T) {
	dir := copyFixture(t, "inputmismatch")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "panel.lsx"),
		Line:       2,
		Col:        26,
		Severity:   compiler.SeverityError,
		Code:       "LSX013",
		Message:    "[input] name: cannot assign Panel.Count (int) to UserCard.Name (string)",
		Suggestion: "bind a Panel field assignable to string",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "panel_gen.go")); !os.IsNotExist(err) {
		t.Errorf("panel_gen.go must not be written for an unassignable input (stat err: %v)", err)
	}
}

func TestBuildReportsMalformedInputExpression(t *testing.T) {
	dir := copyFixture(t, "badinputexpr")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "panel.lsx"),
		Line:       2,
		Col:        26,
		Severity:   compiler.SeverityError,
		Code:       "LSX013",
		Message:    `malformed [input] expression "{{ Owner }}": want a parent field path such as "Owner"`,
		Suggestion: `write [name]="Owner" — input bindings take the field name bare, without braces`,
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "panel_gen.go")); !os.IsNotExist(err) {
		t.Errorf("panel_gen.go must not be written for a malformed input expression (stat err: %v)", err)
	}
}

func TestBuildReportsEventBindingOnChildSelector(t *testing.T) {
	dir := copyFixture(t, "childclick")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "panel.lsx"),
		Line:       2,
		Col:        33,
		Severity:   compiler.SeverityError,
		Code:       "LSX014",
		Message:    "(click) cannot bind on the child selector app-user-card; the element is replaced by the child's own render",
		Suggestion: "move the (click) into the app-user-card component's own template",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "panel_gen.go")); !os.IsNotExist(err) {
		t.Errorf("panel_gen.go must not be written for a binding on a child selector (stat err: %v)", err)
	}
}
