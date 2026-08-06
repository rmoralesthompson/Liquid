package main

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ergonomics_test.go is the ticket #71 Tier A agent-ergonomics harness: a
// deterministic, no-LLM regression net over a representative corpus of
// deliberately-broken components (ADR-0001). Each case runs through the same
// `liquid build|vet --json` seam an agent parses to self-repair, and the
// harness asserts the D13 diagnostic contract holds end to end:
//
//   - the right code at the right file:line:col,
//   - the exact agent-facing JSON field set (renaming a field is breaking),
//   - the LSX017 error-vs-warning split, and
//   - the actionability guarantee that makes a diagnostic self-repairable:
//     every error carries a non-empty message and suggestion (fix-hint).
//
// A refactor that drops a code, moves a line, renames a field, or strips a
// fix-hint fails here on every PR — which is the whole point of measuring
// "designed for agents" instead of asserting it.

// d13Contract is the exact JSON key set of a Diagnostic — the agent-facing API
// (D13). Adding, dropping, or renaming a key is a breaking change this harness
// is meant to catch.
var d13Contract = []string{"code", "col", "file", "line", "message", "severity", "suggestion"}

// ergonomicsCase is one broken-component fixture and the single diagnostic the
// CLI must emit for it. Fixtures are reused from the compiler's testdata (the
// same source the other CLI-seam tests draw on) so the corpus stays in one
// place.
type ergonomicsCase struct {
	name     string // subtest name
	fixture  string // directory under internal/compiler/testdata
	verb     string // build or vet — whichever stage owns the check
	file     string // basename the diagnostic must point at
	line     int
	col      int
	severity string
	code     string
}

// ergonomicsCorpus is the Tier A representative set. It spans a parse-stage
// error (LSX001), a template/struct mismatch (LSX004), two framework-contract
// checks reachable only through vet (LSX011, LSX012), both ends of the D29
// reactivity-leak severity split (LSX017 error and warning), and the D30
// unguarded-payload-action warning (LSX018) — a valid component that draws a
// non-fatal nudge, so the corpus exercises a go/types-positioned warning too.
var ergonomicsCorpus = []ergonomicsCase{
	{"malformed template", "unclosed", "build", "unclosed.lsx", 3, 6, "error", "LSX001"},
	{"unknown field reference", "typo", "vet", "typo.lsx", 3, 10, "error", "LSX004"},
	{"form without CSRF field", "nocsrf", "vet", "nocsrf.lsx", 2, 3, "error", "LSX011"},
	{"unknown child selector", "ghostchild", "vet", "ghost.lsx", 2, 3, "error", "LSX012"},
	{"leaked subscription", "leaks", "vet", "leaky.go", 19, 11, "error", "LSX017"},
	{"captured subscription", "captured", "vet", "captured.go", 21, 41, "warning", "LSX017"},
	{"unguarded payload action", "renamer", "vet", "renamer.go", 16, 19, "warning", "LSX018"},
}

func TestErgonomicsDiagnosticContract(t *testing.T) {
	for _, tc := range ergonomicsCorpus {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", tc.fixture), dir)

			diags := ergonomicsDiagnostics(t, tc.verb, dir)
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want exactly 1: %v", len(diags), diags)
			}
			d := diags[0]

			// The JSON key set is the agent-facing API (D13).
			var keys []string
			for k := range d {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if !slicesEqual(keys, d13Contract) {
				t.Errorf("JSON keys = %v, want %v (D13: field names are the API)", keys, d13Contract)
			}

			// Right code at the right place.
			if got, want := d["file"], filepath.Join(dir, tc.file); got != want {
				t.Errorf("file = %v, want %v", got, want)
			}
			if got := d["line"]; got != float64(tc.line) {
				t.Errorf("line = %v, want %d", got, tc.line)
			}
			if got := d["col"]; got != float64(tc.col) {
				t.Errorf("col = %v, want %d", got, tc.col)
			}
			if got := d["code"]; got != tc.code {
				t.Errorf("code = %v, want %s", got, tc.code)
			}
			if got := d["severity"]; got != tc.severity {
				t.Errorf("severity = %v, want %s", got, tc.severity)
			}

			// Actionability: a diagnostic with no message cannot be understood,
			// and an error with no fix-hint cannot be self-repaired.
			if msg, _ := d["message"].(string); strings.TrimSpace(msg) == "" {
				t.Error("message is empty; an agent cannot understand the failure")
			}
			if tc.severity == "error" {
				if sug, _ := d["suggestion"].(string); strings.TrimSpace(sug) == "" {
					t.Errorf("%s error carries no suggestion; the fix-hint is what makes it self-repairable", tc.code)
				}
			}
		})
	}
}

// ergonomicsDiagnostics runs `liquid <verb> <dir> --json` through the CLI seam
// and returns the decoded D13 array. Error-severity fixtures make run exit
// non-zero, but the JSON array is still written to stdout — that is the
// contract — so the run error is tolerated; only output that cannot be decoded
// as the D13 array is fatal.
func ergonomicsDiagnostics(t *testing.T, verb, dir string) []map[string]any {
	t.Helper()
	var out strings.Builder
	runErr := run([]string{verb, dir, "--json"}, &out)
	var diags []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &diags); err != nil {
		t.Fatalf("%s --json output is not a JSON array (run err: %v): %v\n--- output ---\n%s",
			verb, runErr, err, out.String())
	}
	return diags
}
