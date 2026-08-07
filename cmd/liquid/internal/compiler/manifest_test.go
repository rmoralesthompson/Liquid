package compiler_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// manifestOf builds the manifest graph for a fixture directory, failing the
// test on a mechanical error or on a package that does not compile (the
// broken-package path is exercised by its own test).
func manifestOf(t *testing.T, fixture string) *compiler.ManifestGraph {
	t.Helper()
	graph, diags, err := compiler.Manifest(context.Background(), filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("Manifest(%s): %v", fixture, err)
	}
	if graph == nil {
		t.Fatalf("Manifest(%s): nil graph with diagnostics %+v", fixture, diags)
	}
	return graph
}

func TestManifestReportsFieldsAndInputBindings(t *testing.T) {
	graph := manifestOf(t, "nested")

	if graph.Version == "" {
		t.Errorf("Version = %q, want a non-empty schema version", graph.Version)
	}

	byName := make(map[string]compiler.ManifestComponent, len(graph.Components))
	for _, c := range graph.Components {
		c.File = filepath.Base(c.File)
		byName[c.Selector] = c
	}

	// The graph covers every resolvable component in the dir, sorted by
	// selector.
	var order []string
	for _, c := range graph.Components {
		order = append(order, c.Selector)
	}
	wantOrder := []string{"app-avatar", "app-dashboard", "app-user-card"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("component order = %v, want %v", order, wantOrder)
	}

	// Avatar's Initials is bound as an [input] by user_card.lsx.
	avatar := byName["app-avatar"]
	if avatar.Struct != "Avatar" || avatar.File != "avatar.lsx" {
		t.Errorf("avatar identity = %q/%q, want Avatar/avatar.lsx", avatar.Struct, avatar.File)
	}
	wantAvatarFields := []compiler.ManifestField{{Name: "Initials", Type: "string", Input: true}}
	if !reflect.DeepEqual(avatar.Fields, wantAvatarFields) {
		t.Errorf("avatar.Fields = %+v, want %+v", avatar.Fields, wantAvatarFields)
	}
	if avatar.Interactive {
		t.Errorf("avatar.Interactive = true, want false (no [hydroId])")
	}

	// UserCard's Name is an [input] (bound by dashboard.lsx); Dashboard's own
	// fields are bound by nobody, so neither is an input.
	userCard := byName["app-user-card"]
	wantUserCardFields := []compiler.ManifestField{{Name: "Name", Type: "string", Input: true}}
	if !reflect.DeepEqual(userCard.Fields, wantUserCardFields) {
		t.Errorf("user-card.Fields = %+v, want %+v", userCard.Fields, wantUserCardFields)
	}

	dashboard := byName["app-dashboard"]
	wantDashFields := []compiler.ManifestField{
		{Name: "Title", Type: "string"},
		{Name: "Owner", Type: "string"},
	}
	if !reflect.DeepEqual(dashboard.Fields, wantDashFields) {
		t.Errorf("dashboard.Fields = %+v, want %+v", dashboard.Fields, wantDashFields)
	}
}

func TestManifestReportsClickActionAndHydroRoot(t *testing.T) {
	graph := manifestOf(t, "counter")

	if len(graph.Components) != 1 {
		t.Fatalf("Components = %+v, want exactly one", graph.Components)
	}
	c := graph.Components[0]
	if c.Selector != "app-counter" || !c.Interactive {
		t.Errorf("component = %q interactive=%v, want app-counter interactive=true", c.Selector, c.Interactive)
	}
	wantFields := []compiler.ManifestField{
		{Name: "HydroID", Type: "string"},
		{Name: "Count", Type: "int"},
	}
	if !reflect.DeepEqual(c.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", c.Fields, wantFields)
	}
	wantActions := []compiler.ManifestAction{
		{Name: "Increment", Signature: "func()", Events: []string{"click"}, ClosedDomains: map[string][]string{}},
	}
	if !reflect.DeepEqual(c.Actions, wantActions) {
		t.Errorf("Actions = %+v, want %+v", c.Actions, wantActions)
	}
}

func TestManifestReportsSubmitActionSignature(t *testing.T) {
	graph := manifestOf(t, "renamer")

	c := graph.Components[0]
	wantActions := []compiler.ManifestAction{
		{Name: "Rename", Signature: "func(e liquid.Event)", TakesEvent: true, Events: []string{"submit"}, ClosedDomains: map[string][]string{}},
	}
	if !reflect.DeepEqual(c.Actions, wantActions) {
		t.Errorf("Actions = %+v, want %+v", c.Actions, wantActions)
	}
}

func TestManifestBrokenPackageEmitsDiagnosticsNoGraph(t *testing.T) {
	graph, diags, err := compiler.Manifest(context.Background(), filepath.Join("testdata", "typo"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if graph != nil {
		t.Errorf("graph = %+v, want nil for a package that does not compile", graph)
	}
	var hasError bool
	for _, d := range diags {
		if d.Severity == compiler.SeverityError {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("diagnostics = %+v, want at least one error-severity diagnostic", diags)
	}
}

func TestManifestEmptyDirIsEmptyGraph(t *testing.T) {
	graph, diags, err := compiler.Manifest(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if graph == nil {
		t.Fatalf("graph = nil, want an empty graph")
	}
	if len(diags) != 0 {
		t.Errorf("diagnostics = %+v, want none", diags)
	}
	if graph.Version == "" {
		t.Errorf("Version = %q, want a schema version even when empty", graph.Version)
	}
	if len(graph.Components) != 0 {
		t.Errorf("Components = %+v, want empty", graph.Components)
	}
}

func TestManifestSurfacesInputAndChangeEvents(t *testing.T) {
	graph := manifestOf(t, "field")

	events := make(map[string][]string)
	for _, c := range graph.Components {
		for _, a := range c.Actions {
			events[a.Name] = a.Events
		}
	}
	if got := events["Typed"]; !reflect.DeepEqual(got, []string{"input"}) {
		t.Errorf("Typed events = %v, want [input]", got)
	}
	if got := events["Committed"]; !reflect.DeepEqual(got, []string{"change"}) {
		t.Errorf("Committed events = %v, want [change]", got)
	}
}
