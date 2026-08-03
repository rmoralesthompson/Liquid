package compiler_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

func TestLiquidDeferCompilesToSlotWithFallback(t *testing.T) {
	dir := copyFixture(t, "deferred")

	if diags := build(t, dir); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	tmplText := generatedTemplateText(t, filepath.Join(dir, "metrics_page_gen.go"))

	// The usage site becomes a slot element: the liquidDefer call mints the
	// patch-boundary token as the slot's data-hydro-id, and the element body
	// survives as the fallback, transformed like any template content.
	if want := `<div data-hydro-id="{{liquidDefer "app-slow-stats" "range" .Days}}">`; !strings.Contains(tmplText, want) {
		t.Errorf("generated template missing defer slot %q\n--- template ---\n%s", want, tmplText)
	}
	if want := `<p>Loading {{ .Title }}…</p>`; !strings.Contains(tmplText, want) {
		t.Errorf("generated template lost the fallback content %q\n--- template ---\n%s", want, tmplText)
	}
	if strings.Contains(tmplText, "<app-slow-stats") {
		t.Errorf("deferred child element must not survive into the generated template\n--- template ---\n%s", tmplText)
	}
	if strings.Contains(tmplText, "liquidChild") {
		t.Errorf("a deferred occurrence must compile to liquidDefer, not liquidChild\n--- template ---\n%s", tmplText)
	}
}

func TestBuildReportsLiquidDeferOffAChildSelector(t *testing.T) {
	dir := copyFixture(t, "straydefer")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "banner.lsx"),
		Line:       1,
		Col:        21,
		Severity:   compiler.SeverityError,
		Code:       "LSX015",
		Message:    "*liquidDefer must sit on a child-selector element; only a nested component occurrence can render deferred",
		Suggestion: "defer a nested component usage such as <app-stats *liquidDefer>, not a plain element",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "banner_gen.go")); !os.IsNotExist(err) {
		t.Errorf("banner_gen.go must not be written for a misplaced *liquidDefer (stat err: %v)", err)
	}
}

func TestBuildReportsDeferredComponentWithoutHydroID(t *testing.T) {
	dir := copyFixture(t, "defernohydro")

	diags := build(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "page.lsx"),
		Line:       2,
		Col:        19,
		Severity:   compiler.SeverityError,
		Code:       "LSX016",
		Message:    "app-plain-card is deferred but PlainCard has no HydroID string field for the swap to target",
		Suggestion: "add HydroID string to the PlainCard struct",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

func TestBuildReportsLiquidDeferWithAValue(t *testing.T) {
	dir := copyFixture(t, "deferexpr")

	diags := build(t, dir)

	if len(diags) != 1 || diags[0].Code != "LSX005" {
		t.Fatalf("diagnostics = %+v, want exactly one LSX005 for *liquidDefer carrying a value", diags)
	}
	if !strings.Contains(diags[0].Message, "*liquidDefer") {
		t.Errorf("LSX005 message %q does not name *liquidDefer", diags[0].Message)
	}
}
