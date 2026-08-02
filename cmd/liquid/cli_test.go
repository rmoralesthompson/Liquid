package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// typoFixture copies the compiler's typo fixture (one LSX004 at typo.lsx:3:10,
// the misspelled identifier itself) into a temp dir.
func typoFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "typo"), dir)
	return dir
}

func TestBuildJSONEmitsTheD13DiagnosticContract(t *testing.T) {
	dir := typoFixture(t)

	var out strings.Builder
	if err := run([]string{"build", dir, "--json"}, &out); err == nil {
		t.Fatal("run must return an error (non-zero exit) when diagnostics exist")
	}

	var diags []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &diags); err != nil {
		t.Fatalf("--json output is not a JSON array: %v\n--- output ---\n%s", err, out.String())
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}

	d := diags[0]
	var keys []string
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"code", "col", "file", "line", "message", "severity", "suggestion"}; !slicesEqual(keys, want) {
		t.Errorf("JSON keys = %v, want %v (D13: field names are the API)", keys, want)
	}

	for field, want := range map[string]any{
		"file":       filepath.Join(dir, "typo.lsx"),
		"line":       float64(3),
		"col":        float64(10),
		"severity":   "error",
		"code":       "LSX004",
		"message":    "Typo has no field or method named Nmae",
		"suggestion": "did you mean Name?",
	} {
		if d[field] != want {
			t.Errorf("%s = %v, want %v", field, d[field], want)
		}
	}
}

func TestVetDefaultsToHumanReadableTextAndFails(t *testing.T) {
	dir := typoFixture(t)

	var out strings.Builder
	if err := run([]string{"vet", dir}, &out); err == nil {
		t.Fatal("run must return an error (non-zero exit) when diagnostics exist")
	}

	want := filepath.Join(dir, "typo.lsx") +
		":3:10: error[LSX004]: Typo has no field or method named Nmae (suggestion: did you mean Name?)\n"
	if got := out.String(); got != want {
		t.Errorf("human output = %q, want %q", got, want)
	}
}

func TestVetJSONOnCleanInputEmitsAnEmptyArray(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "hello"), dir)

	var out strings.Builder
	if err := run([]string{"vet", dir, "--json"}, &out); err != nil {
		t.Fatalf("vet on clean input: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("--json output on clean input = %q, want %q (agents always get a parseable array)", got, "[]")
	}
}

func TestBuildFailsWithDiagnosticOnMalformedLSX(t *testing.T) {
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", "unclosed"), dir)

	var out strings.Builder
	if err := run([]string{"build", dir}, &out); err == nil {
		t.Fatal("malformed .lsx must make the CLI exit non-zero")
	}
	if got := out.String(); !strings.Contains(got, "error[LSX001]: unclosed interpolation") {
		t.Errorf("output = %q, want an LSX001 diagnostic line", got)
	}
}

func TestRunRejectsUnknownFlagsAndExtraArguments(t *testing.T) {
	if err := run([]string{"vet", "--jsn", "."}, io.Discard); err == nil || !strings.Contains(err.Error(), `unknown flag "--jsn"`) {
		t.Errorf("unknown flag error = %v, want it to name the flag, not treat it as the directory", err)
	}
	if err := run([]string{"vet", "a", "b"}, io.Discard); err == nil || !strings.Contains(err.Error(), `unexpected argument "b"`) {
		t.Errorf("extra positional error = %v, want it to reject the second argument, not overwrite the directory", err)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
