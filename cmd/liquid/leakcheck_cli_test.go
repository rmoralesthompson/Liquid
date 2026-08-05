package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestVetJSONSurfacesTheReactivityLeak is the CLI-seam check for D29: liquid
// vet on a component with a bare Subscribe fails the invocation and emits the
// LSX017 leak through the same D13 --json array agents already parse.
func TestVetJSONSurfacesTheReactivityLeak(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "leaks"), dir)

	var out strings.Builder
	if err := run([]string{"vet", dir, "--json"}, &out); err == nil {
		t.Fatal("run must return an error (non-zero exit) when a leak is found")
	}

	var diags []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &diags); err != nil {
		t.Fatalf("--json output is not a JSON array: %v\n--- output ---\n%s", err, out.String())
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}

	d := diags[0]
	for field, want := range map[string]any{
		"file":     filepath.Join(dir, "leaky.go"),
		"line":     float64(19),
		"col":      float64(11),
		"severity": "error",
		"code":     "LSX017",
	} {
		if d[field] != want {
			t.Errorf("%s = %v, want %v", field, d[field], want)
		}
	}
}

// TestVetWarningIsReportedButDoesNotFail covers the warning-vs-error exit
// contract: a captured (non-provable) leak is a warning — printed and exposed
// to agents, but it does not fail the invocation. Only a provable-leak error
// does (asserted by TestVetJSONSurfacesTheReactivityLeak).
func TestVetWarningIsReportedButDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "captured"), dir)

	var out strings.Builder
	if err := run([]string{"vet", dir}, &out); err != nil {
		t.Fatalf("a warning-only vet must exit 0, got error: %v", err)
	}
	if !strings.Contains(out.String(), "warning[LSX017]") {
		t.Errorf("the warning must still be printed; output = %q", out.String())
	}
}
