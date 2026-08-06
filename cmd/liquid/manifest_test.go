package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// manifestFixture copies a compiler fixture package into a temp dir so the CLI
// runs against a real, self-contained module (as build/vet tests do).
func manifestFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	copyFixtureDir(t, filepath.Join("internal", "compiler", "testdata", name), dir)
	return dir
}

func TestManifestJSONEmitsAStableEnvelope(t *testing.T) {
	dir := manifestFixture(t, "counter")

	var out strings.Builder
	if err := run([]string{"manifest", dir, "--json"}, &out); err != nil {
		t.Fatalf("manifest --json on a clean package: %v", err)
	}

	var graph map[string]any
	if err := json.Unmarshal([]byte(out.String()), &graph); err != nil {
		t.Fatalf("--json output is not a JSON object: %v\n--- output ---\n%s", err, out.String())
	}

	// The field names are the API (D26): pin the envelope shape.
	if keys := sortedKeys(graph); !slicesEqual(keys, []string{"components", "version"}) {
		t.Errorf("envelope keys = %v, want [components version]", keys)
	}
	if graph["version"] != "v0" {
		t.Errorf("version = %v, want v0", graph["version"])
	}

	comps, ok := graph["components"].([]any)
	if !ok || len(comps) != 1 {
		t.Fatalf("components = %v, want exactly one", graph["components"])
	}
	comp := comps[0].(map[string]any)
	if keys := sortedKeys(comp); !slicesEqual(keys, []string{"actions", "fields", "file", "head", "interactive", "selector", "struct"}) {
		t.Errorf("component keys = %v, want [actions fields file head interactive selector struct]", keys)
	}
	if comp["selector"] != "app-counter" || comp["interactive"] != true {
		t.Errorf("component = %v, want app-counter interactive", comp)
	}

	action := comp["actions"].([]any)[0].(map[string]any)
	if keys := sortedKeys(action); !slicesEqual(keys, []string{"closedDomains", "events", "guard", "name", "signature", "takesEvent"}) {
		t.Errorf("action keys = %v, want [closedDomains events guard name signature takesEvent]", keys)
	}
	if action["name"] != "Increment" || action["signature"] != "func()" || action["takesEvent"] != false {
		t.Errorf("action = %v, want Increment func() takesEvent=false", action)
	}
	if action["guard"] != false {
		t.Errorf("action guard = %v, want false: Increment declares no payload guard (D30)", action["guard"])
	}
}

func TestManifestJSONReportsInputFieldNess(t *testing.T) {
	dir := manifestFixture(t, "nested")

	var out strings.Builder
	if err := run([]string{"manifest", dir, "--json"}, &out); err != nil {
		t.Fatalf("manifest --json on nested: %v", err)
	}

	var graph struct {
		Components []struct {
			Selector string
			Fields   []struct {
				Name  string
				Input bool
			}
		}
	}
	if err := json.Unmarshal([]byte(out.String()), &graph); err != nil {
		t.Fatalf("decoding manifest: %v", err)
	}

	inputs := map[string]bool{}
	for _, c := range graph.Components {
		for _, f := range c.Fields {
			if f.Input {
				inputs[c.Selector+"."+f.Name] = true
			}
		}
	}
	for _, want := range []string{"app-avatar.Initials", "app-user-card.Name"} {
		if !inputs[want] {
			t.Errorf("%s not marked as an [input] field; inputs=%v", want, inputs)
		}
	}
	if inputs["app-dashboard.Title"] {
		t.Errorf("dashboard.Title marked as input, but nothing binds it")
	}
}

func TestManifestEmptyFieldsAndActionsAreArraysNotNull(t *testing.T) {
	// A component with no fields and no actions must still serialize both as
	// [], so an agent never has to special-case null (the [] contract the
	// top-level graph gives).
	dir := manifestFixture(t, "hello")

	var out strings.Builder
	if err := run([]string{"manifest", dir, "--json"}, &out); err != nil {
		t.Fatalf("manifest --json on hello: %v", err)
	}
	if got := out.String(); strings.Contains(got, `"fields":null`) || strings.Contains(got, `"actions":null`) {
		t.Errorf("empty slices serialized as null, want []: %s", got)
	}
	if !strings.Contains(out.String(), `"actions":[]`) {
		t.Errorf("output = %s, want an empty actions array", out.String())
	}
}

func TestManifestTextIsHumanReadable(t *testing.T) {
	dir := manifestFixture(t, "counter")

	var out strings.Builder
	if err := run([]string{"manifest", dir}, &out); err != nil {
		t.Fatalf("manifest (text) on counter: %v", err)
	}
	got := out.String()
	for _, want := range []string{"app-counter (Counter)", "[interactive]", "Increment", "func()", "(click)"} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

func TestManifestBrokenPackageEmitsDiagnosticsAndFails(t *testing.T) {
	dir := typoFixture(t)

	var out strings.Builder
	if err := run([]string{"manifest", dir}, &out); err == nil {
		t.Fatal("manifest on a package that does not compile must exit non-zero")
	}
	if got := out.String(); !strings.Contains(got, "error[LSX004]") {
		t.Errorf("output = %q, want the D13 diagnostic, no manifest", got)
	}
	if strings.Contains(out.String(), "\"components\"") {
		t.Errorf("a broken package must not emit a manifest, got %q", out.String())
	}
}

func TestManifestEmptyDirEmitsEmptyGraph(t *testing.T) {
	var out strings.Builder
	if err := run([]string{"manifest", t.TempDir(), "--json"}, &out); err != nil {
		t.Fatalf("manifest --json on an empty dir: %v", err)
	}
	var graph struct {
		Version    string
		Components []any
	}
	if err := json.Unmarshal([]byte(out.String()), &graph); err != nil {
		t.Fatalf("decoding manifest: %v", err)
	}
	if graph.Version != "v0" || len(graph.Components) != 0 {
		t.Errorf("empty-dir manifest = %+v, want version v0 with no components", graph)
	}
}

func TestManifestRejectsUnknownFlagsAndExtraArguments(t *testing.T) {
	if err := run([]string{"manifest", "--jsn", "."}, io.Discard); err == nil || !strings.Contains(err.Error(), `unknown flag "--jsn"`) {
		t.Errorf("unknown flag error = %v, want it to name the flag", err)
	}
	if err := run([]string{"manifest", "a", "b"}, io.Discard); err == nil || !strings.Contains(err.Error(), `unexpected argument "b"`) {
		t.Errorf("extra positional error = %v, want it rejected", err)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
