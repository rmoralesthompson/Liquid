package compiler_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

func TestClickBindingCompilesToActionAttributeAndAllowlist(t *testing.T) {
	dir := copyFixture(t, "counter")

	if diags := build(t, dir); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	generated, err := os.ReadFile(filepath.Join(dir, "counter_gen.go"))
	if err != nil {
		t.Fatalf("expected counter_gen.go beside the source: %v", err)
	}
	got := string(generated)

	if want := `data-liquid-action=\"Increment\"`; !strings.Contains(got, want) {
		t.Errorf("generated template missing %q\n--- generated ---\n%s", want, got)
	}
	if strings.Contains(got, "(click)") {
		t.Errorf("(click) binding must not survive into the generated template\n--- generated ---\n%s", got)
	}
	for _, want := range []string{
		"func (c *Counter) Actions() []string",
		`"Increment"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated allowlist missing %q\n--- generated ---\n%s", want, got)
		}
	}
}

func TestBuildReportsUnknownClickHandlerWithSuggestion(t *testing.T) {
	dir := copyFixture(t, "clicktypo")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "clicktypo.lsx"),
		Line:       2,
		Col:        20,
		Severity:   compiler.SeverityError,
		Code:       "LSX004",
		Message:    "Clicktypo has no field or method named Incrment",
		Suggestion: "did you mean Increment?",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "clicktypo_gen.go")); !os.IsNotExist(err) {
		t.Errorf("clicktypo_gen.go must not be written when a handler reference fails vet (stat err: %v)", err)
	}
}

func TestBuildReportsClickHandlerWithWrongSignature(t *testing.T) {
	dir := copyFixture(t, "badsig")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "badsig.lsx"),
		Line:       2,
		Col:        20,
		Severity:   compiler.SeverityError,
		Code:       "LSX008",
		Message:    "(click) handler Increment has signature func(n int); a handler is func() or func(e liquid.Event) (D11)",
		Suggestion: "change the method to func (c *Badsig) Increment() or func (c *Badsig) Increment(e liquid.Event)",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "badsig_gen.go")); !os.IsNotExist(err) {
		t.Errorf("badsig_gen.go must not be written for an invalid handler (stat err: %v)", err)
	}
}

func TestBuildReportsEventHandlerWithResultAsInvalid(t *testing.T) {
	dir := copyFixture(t, "badevent")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "badevent.lsx"),
		Line:       2,
		Col:        19,
		Severity:   compiler.SeverityError,
		Code:       "LSX008",
		Message:    "(submit) handler Rename has signature func(e liquid.Event) error; a handler is func() or func(e liquid.Event) (D11)",
		Suggestion: "change the method to func (c *Badevent) Rename() or func (c *Badevent) Rename(e liquid.Event)",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "badevent_gen.go")); !os.IsNotExist(err) {
		t.Errorf("badevent_gen.go must not be written for an invalid handler (stat err: %v)", err)
	}
}

func TestBuildReportsHydroIdWithoutHydroIDField(t *testing.T) {
	dir := copyFixture(t, "nohydrofield")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "nohydrofield.lsx"),
		Line:       1,
		Col:        6,
		Severity:   compiler.SeverityError,
		Code:       "LSX009",
		Message:    "Nohydrofield uses [hydroId] but has no HydroID string field for the framework to fill",
		Suggestion: "add HydroID string to the Nohydrofield struct",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "nohydrofield_gen.go")); !os.IsNotExist(err) {
		t.Errorf("nohydrofield_gen.go must not be written without hydro plumbing (stat err: %v)", err)
	}
}

func TestBuildReportsClickBindingWithoutHydroIdRoot(t *testing.T) {
	dir := copyFixture(t, "strayclick")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "strayclick.lsx"),
		Line:       1,
		Col:        9,
		Severity:   compiler.SeverityError,
		Code:       "LSX010",
		Message:    "(click) needs a patch boundary, but no element in strayclick.lsx declares [hydroId]",
		Suggestion: "add [hydroId] to the component's root element",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "strayclick_gen.go")); !os.IsNotExist(err) {
		t.Errorf("strayclick_gen.go must not be written without a patch root (stat err: %v)", err)
	}
}

func TestSubmitBindingCompilesToSubmitAttributeAndAllowlist(t *testing.T) {
	dir := copyFixture(t, "renamer")

	// renamer's Rename is an unguarded payload action, so it draws an LSX018
	// warning (D30); that is expected and non-fatal — only an error would stop
	// this binding from compiling.
	for _, d := range build(t, dir) {
		if d.Severity == compiler.SeverityError {
			t.Fatalf("unexpected error diagnostic: %+v", d)
		}
	}

	generated, err := os.ReadFile(filepath.Join(dir, "renamer_gen.go"))
	if err != nil {
		t.Fatalf("expected renamer_gen.go beside the source: %v", err)
	}
	got := string(generated)

	if want := `data-liquid-submit=\"Rename\"`; !strings.Contains(got, want) {
		t.Errorf("generated template missing %q\n--- generated ---\n%s", want, got)
	}
	if strings.Contains(got, "(submit)") {
		t.Errorf("(submit) binding must not survive into the generated template\n--- generated ---\n%s", got)
	}
	for _, want := range []string{
		"func (c *Renamer) Actions() []string",
		`"Rename"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated allowlist missing %q\n--- generated ---\n%s", want, got)
		}
	}
}

func TestFormReceivesAutoInjectedCSRFTokenInput(t *testing.T) {
	dir := copyFixture(t, "renamer")

	// The unguarded-action LSX018 warning (D30) is expected here and non-fatal;
	// only an error would stop the CSRF input from being injected.
	for _, d := range build(t, dir) {
		if d.Severity == compiler.SeverityError {
			t.Fatalf("unexpected error diagnostic: %+v", d)
		}
	}

	tmplText := generatedTemplateText(t, filepath.Join(dir, "renamer_gen.go"))

	type data struct {
		HydroID   string
		CSRFToken string
		Title     string
	}
	got := execute(t, tmplText, data{HydroID: "tok", CSRFToken: "sess:99:sig", Title: "Ops"})
	want := `<input type="hidden" name="csrf_token" value="sess:99:sig"/>`
	if !strings.Contains(got, want) {
		t.Errorf("rendered form missing auto-injected CSRF input %q\n--- rendered ---\n%s", want, got)
	}
	if formEnd := strings.Index(got, "</form>"); formEnd < 0 || strings.Index(got, want) > formEnd {
		t.Errorf("CSRF input must render inside the form\n--- rendered ---\n%s", got)
	}
}

func TestBuildReportsFormWithoutCSRFTokenField(t *testing.T) {
	dir := copyFixture(t, "nocsrf")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "nocsrf.lsx"),
		Line:       2,
		Col:        3,
		Severity:   compiler.SeverityError,
		Code:       "LSX011",
		Message:    "Nocsrf has a <form> but no CSRFToken string field for the framework to fill",
		Suggestion: "add CSRFToken string to the Nocsrf struct",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "nocsrf_gen.go")); !os.IsNotExist(err) {
		t.Errorf("nocsrf_gen.go must not be written without CSRF plumbing (stat err: %v)", err)
	}
}

func TestBuildReportsSubmitBindingWithoutHydroIdRoot(t *testing.T) {
	dir := copyFixture(t, "straysubmit")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "straysubmit.lsx"),
		Line:       2,
		Col:        9,
		Severity:   compiler.SeverityError,
		Code:       "LSX010",
		Message:    "(submit) needs a patch boundary, but no element in straysubmit.lsx declares [hydroId]",
		Suggestion: "add [hydroId] to the component's root element",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "straysubmit_gen.go")); !os.IsNotExist(err) {
		t.Errorf("straysubmit_gen.go must not be written without a patch root (stat err: %v)", err)
	}
}

func TestHydroIdCompilesToDataHydroIdInterpolation(t *testing.T) {
	dir := copyFixture(t, "counter")

	if diags := build(t, dir); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	tmplText := generatedTemplateText(t, filepath.Join(dir, "counter_gen.go"))
	if strings.Contains(strings.ToLower(tmplText), "[hydroid]") {
		t.Errorf("[hydroId] must not survive into the generated template\n--- template ---\n%s", tmplText)
	}

	type data struct {
		HydroID string
		Count   int
	}
	got := execute(t, tmplText, data{HydroID: "tok123", Count: 7})
	for _, want := range []string{
		`data-hydro-id="tok123"`,
		`<span id="count">7</span>`,
		`data-liquid-action="Increment"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q\n--- rendered ---\n%s", want, got)
		}
	}
}
