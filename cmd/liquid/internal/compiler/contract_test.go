package compiler_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

func TestBuildEmitsPayloadDomainsFromActionGuard(t *testing.T) {
	dir := copyFixture(t, "mover")

	diags := build(t, dir)

	for _, d := range diags {
		if d.Severity == compiler.SeverityError {
			t.Fatalf("unexpected error diagnostic building mover: %+v", d)
		}
	}
	gen, err := os.ReadFile(filepath.Join(dir, "mover_gen.go"))
	if err != nil {
		t.Fatalf("expected mover_gen.go beside the source: %v", err)
	}
	got := string(gen)
	// The guard's MovePayload.Dir field is a Direction const-set, so the seam
	// contract enumerates it; Step is an unbounded int and carries no domain.
	for _, want := range []string{
		"func (c *Mover) PayloadDomains() map[string]map[string][]string {",
		`"Move": {`,
		`"dir": {"down", "up"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mover_gen.go missing %q\n--- generated ---\n%s", want, got)
		}
	}
	if strings.Contains(got, `"step"`) {
		t.Errorf("mover_gen.go constrains step, but an unbounded int is no closed domain\n--- generated ---\n%s", got)
	}
}

func TestVetWarnsUnguardedPayloadAction(t *testing.T) {
	dir := copyFixture(t, "renamer")

	diags := vet(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "renamer.go"),
		Line:       16,
		Col:        19,
		Severity:   compiler.SeverityWarning,
		Code:       "LSX018",
		Message:    "action Rename takes a client payload but declares no guard, so nothing constrains its payload values at the dispatch seam (D30)",
		Suggestion: "add func (c *Renamer) RenameGuard(p <Payload>) bool to refuse an out-of-contract payload before the handler runs; a closed-domain (enum) payload field is enforced at the seam only when the action declares this guard (D30, ADR-0003)",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

// TestUnguardedClosedDomainIsNotEnforced pins the documented v0.1 gap #85 /
// ADR-0003: a closed-domain enum field on an action that declares no guard is
// invisible to the contract compiler (the payload struct is named to go/types
// only through a guard's parameter), so it is neither enumerated into
// PayloadDomains nor enforced at the seam — the author gets the LSX018
// unguarded-action warning instead. If typed-payload handlers later make the
// payload discoverable without a guard, this expectation should flip.
func TestUnguardedClosedDomainIsNotEnforced(t *testing.T) {
	dir := copyFixture(t, "prefs")

	// Build: prefs declares Level (a closed const-set) and a SetPriorityPayload,
	// but SetPriority has no guard, so nothing associates that payload with the
	// action. No PayloadDomains is generated, and "low"/"high" are not enforced.
	for _, d := range build(t, dir) {
		if d.Severity == compiler.SeverityError {
			t.Fatalf("unexpected error diagnostic building prefs: %+v", d)
		}
	}
	if gen, err := os.ReadFile(filepath.Join(dir, "prefs_gen.go")); err == nil {
		got := string(gen)
		if strings.Contains(got, "PayloadDomains") {
			t.Errorf("prefs_gen.go generated PayloadDomains for an unguarded action; the closed domain should be invisible without a guard\n--- generated ---\n%s", got)
		}
		for _, leak := range []string{`"low"`, `"high"`, `"level"`} {
			if strings.Contains(got, leak) {
				t.Errorf("prefs_gen.go enumerated %s, but an unguarded closed domain must not be enforced\n--- generated ---\n%s", leak, got)
			}
		}
	}

	// Vet: the author's only signal is the generic unguarded-action warning,
	// whose sharpened suggestion now names the closed-domain coupling.
	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "prefs.go"),
		Line:       38,
		Col:        17,
		Severity:   compiler.SeverityWarning,
		Code:       "LSX018",
		Message:    "action SetPriority takes a client payload but declares no guard, so nothing constrains its payload values at the dispatch seam (D30)",
		Suggestion: "add func (c *Prefs) SetPriorityGuard(p <Payload>) bool to refuse an out-of-contract payload before the handler runs; a closed-domain (enum) payload field is enforced at the seam only when the action declares this guard (D30, ADR-0003)",
	}}
	if diags := vet(t, dir); !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

func TestVetSilentOnGuardedPayloadAction(t *testing.T) {
	dir := copyFixture(t, "mover")

	// Mover's Move declares a MoveGuard, so the payload axis is covered and the
	// unguarded-action warning must not fire (D30).
	for _, d := range vet(t, dir) {
		if d.Code == "LSX018" {
			t.Errorf("unexpected LSX018 on a guarded action: %+v", d)
		}
	}
}

func TestManifestSurfacesGuardAndClosedDomains(t *testing.T) {
	graph := manifestOf(t, "mover")

	if len(graph.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(graph.Components))
	}
	wantActions := []compiler.ManifestAction{{
		Name:          "Move",
		Signature:     "func(e liquid.Event)",
		TakesEvent:    true,
		Events:        []string{"click"},
		Guard:         true,
		ClosedDomains: map[string][]string{"Dir": {"down", "up"}},
	}}
	if !reflect.DeepEqual(graph.Components[0].Actions, wantActions) {
		t.Errorf("Actions = %+v, want %+v", graph.Components[0].Actions, wantActions)
	}
}
